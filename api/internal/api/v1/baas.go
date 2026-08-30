package v1

import (
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/middleware"
	"gopkg.aoctech.app/wallet/api/internal/pix"
	"gopkg.aoctech.app/wallet/api/internal/problem"
	"gopkg.aoctech.app/wallet/api/internal/services"
)

// initiateClosure starts the account-closure state machine (plan §7.2).
func (h *handlers) initiateClosure(c fiber.Ctx) error {
	cl := middleware.GetClaims(c)
	idemKey, p := requireIdempotencyKey(c)
	if p != nil {
		return sendProblem(c, p)
	}
	acc, err := h.svc.InitiateClosure(c.Context(), cl.Sub, idemKey)
	if err != nil {
		return sendProblem(c, err)
	}
	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{"status": acc.Status})
}

// initiateOnboarding starts custody onboarding: it reserves the caller's
// record and opens the verification-fee charge. The subaccount itself is only
// created once that fee clears, because the provider bills at creation.
func (h *handlers) initiateOnboarding(c fiber.Ctx) error {
	var body OnboardingRequest
	if p := bindJSON(c, &body); p != nil {
		return sendProblem(c, p)
	}
	cl := middleware.GetClaims(c)
	allowed, err := h.svc.CustodyEnabledForUser(c.Context(), cl.Sub)
	if err != nil {
		return sendProblem(c, err)
	}
	if !allowed {
		// The allowlist is operational state, not an enrollment feature.
		return sendProblem(c, problem.NotFound("recurso não encontrado"))
	}
	acc, charge, err := h.baas.RequestCustodyAccount(c.Context(), cl.Sub, cl.KYCLevel, body.IncomeValue)
	if err != nil {
		return sendProblem(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(onboardingResponse(acc, charge))
}

// getOnboarding is the state the onboarding screen polls. Read-only: it opens
// no charge and creates no subaccount.
func (h *handlers) getOnboarding(c fiber.Ctx) error {
	cl := middleware.GetClaims(c)
	acc, err := h.baas.OnboardingState(c.Context(), cl.Sub)
	if err != nil {
		return sendProblem(c, err)
	}
	if acc == nil {
		return sendProblem(c, problem.NotFound("onboarding não iniciado"))
	}
	// An outstanding fee is part of the state, not a side effect of asking for
	// it: this reads the stored charge and opens nothing.
	return c.JSON(onboardingResponse(acc, h.baas.OutstandingFeeCharge(acc)))
}

func onboardingResponse(acc *wallet.BaasAccount, charge *pix.Charge) OnboardingResponse {
	out := OnboardingResponse{Status: acc.Status, OnboardingURL: acc.OnboardingURL}
	if charge != nil {
		out.Fee = &OnboardingFee{
			Amount: charge.Amount, QRCode: charge.QRCode, QRCodeB64: charge.QRCodeB64,
			// Stated in the response rather than only in the terms: the provider
			// consumes this fee at subaccount creation and does not return it if
			// registration is later refused.
			Refundable: false,
		}
	}
	return out
}

// asaasWebhookPayload covers Asaas's account-status callback. Other event
// families (PAYMENT_RECEIVED, MED clawback, balance-block/freeze) are added
// to the switch in asaasWebhook as their owning plan sections are built (§4,
// §7) — this dispatcher is the single entry point for all of them, never one
// route per event type. Field names/shape are best-effort, same "confirm
// before build" posture as every other Asaas payload in this plan.
type asaasWebhookPayload struct {
	Event   string `json:"event"`
	Account struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"account"`
	// Payment is a wake-up only. Its ID and external reference tell the API
	// which provider records to re-query; its amount/customer are never trusted.
	Payment struct {
		ID                string `json:"id"`
		CustomerID        string `json:"customer"`
		PixQRCodeID       string `json:"pixQrCodeId"`
		ExternalReference string `json:"externalReference"`
	} `json:"payment"`
	// MED clawback (plan §7.3) — a DEDICATED event, never inferred from the
	// conservation-check discovering a mismatch after the fact. Resolves via
	// the top-level Account.ID field above (Asaas events carry the owning
	// account at the top level regardless of event type — unverified
	// assumption, same "confirm before build" posture as the rest of this
	// file). Field names/shape unverified against a live payload.
	Transfer struct {
		ID                string        `json:"id"`
		Value             asaasCentavos `json:"value"`
		ExternalReference string        `json:"externalReference"`
	} `json:"transfer"`
}

// asaasWebhook is the single inbound route for every Asaas event other than
// the synchronous transfer-authorization callback (which has its own route
// and latency budget — see asaasTransferAuthorization). Always acknowledges
// with 200 on a parse failure (logged loudly) rather than erroring, since
// Asaas's redelivery/backoff behavior for this webhook family is unverified
// and a malformed payload must never become a retry storm.
func (h *handlers) asaasWebhook(c fiber.Ctx) error {
	var body asaasWebhookPayload
	if err := json.Unmarshal(c.Body(), &body); err != nil {
		slog.WarnContext(c.Context(), "asaas webhook: bad json", "err", err)
		return c.SendStatus(fiber.StatusOK)
	}
	switch {
	case strings.HasPrefix(body.Event, "ACCOUNT_STATUS_"):
		// The event only says "look again": the outcome comes from
		// GET /v3/myAccount/status, never from this payload. A static webhook
		// token must not be able to approve a custody account on its own.
		if err := h.baas.ProcessAccountStatusWebhook(c.Context(), body.Account.ID); err != nil {
			// 500 so Asaas redelivers — a transient provider read must not be
			// swallowed as an acknowledged event, or the account stays stuck in
			// whatever state it was in with nothing left to re-trigger it.
			slog.ErrorContext(c.Context(), "asaas account-status processing failed",
				"event", body.Event, "account_id", body.Account.ID, "err", err)
			return sendProblem(c, problem.InternalServer("falha ao processar situação da conta"))
		}
	case body.Event == "PAYMENT_RECEIVED" || body.Event == "PAYMENT_CONFIRMED":
		// Two kinds of inbound payment arrive on this one route: a user deposit
		// into their own subaccount, and the verification fee into CTech's
		// master account. The reference prefix is what separates them — never
		// the amount, and never which account the webhook claims to be about.
		if services.IsCustodyFeeReference(body.Payment.ExternalReference) {
			if err := h.baas.ConfirmCustodyFee(c.Context(), body.Payment.ID, body.Payment.ExternalReference); err != nil {
				return sendProblem(c, err)
			}
			break
		}
		if err := h.svc.ConfirmAsaasDeposit(c.Context(), body.Payment.ID, body.Payment.ExternalReference); err != nil {
			return sendProblem(c, err)
		}
	case body.Event == "TRANSFER_MED_CLAWBACK" || body.Event == "PIX_MED_RETURNED":
		// No provider-side MED query is implemented. Debiting from a bearer-token
		// webhook would make that webhook the source of truth for money movement.
		slog.ErrorContext(c.Context(), "ALARM Asaas MED webhook quarantined pending authoritative re-query", "account_id", body.Account.ID, "reference", body.Transfer.ExternalReference)
	default:
		slog.WarnContext(c.Context(), "asaas webhook: unhandled event", "event", body.Event)
	}
	return c.SendStatus(fiber.StatusOK)
}
