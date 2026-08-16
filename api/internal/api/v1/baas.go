package v1

import (
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/wallet/api/internal/middleware"
	"gopkg.aoctech.app/wallet/api/internal/problem"
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

// initiateOnboarding opens the caller's Asaas subaccount (plan §3.2).
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
	acc, err := h.baas.InitiateOnboarding(c.Context(), cl.Sub, cl.KYCLevel, body.IncomeValue)
	if err != nil {
		return sendProblem(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": acc.Status})
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
		// The current Asaas client has no authoritative account-status query.
		// A static webhook token alone must never approve/reject custody. Keep the
		// account pending and page operations until provider re-query exists.
		slog.ErrorContext(c.Context(), "ALARM Asaas account-status webhook quarantined pending authoritative re-query", "event", body.Event, "account_id", body.Account.ID)
	case body.Event == "PAYMENT_RECEIVED" || body.Event == "PAYMENT_CONFIRMED":
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
