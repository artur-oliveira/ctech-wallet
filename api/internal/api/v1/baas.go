package v1

import (
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/wallet/api/internal/middleware"
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
	// Payment covers the deposit-confirmation family (plan §4.3). Payer.CPFCNPJ
	// is the field the CPF anti-fraud gate reads — field name unverified against
	// a live payload, same "confirm before build" posture as the rest of this
	// struct; plan §4.3 itself flags that Asaas's payment RE-QUERY may not
	// expose payer CPF at all, in which case the gate must come from here
	// (the webhook body) exclusively, same as Inter today.
	Payment struct {
		// PixQRCodeID reads Asaas's payment "id" field, not a distinct
		// "pixQrCodeId" — the fetched PAYMENT_RECEIVED example payload
		// (docs.asaas.com/docs/webhook-para-cobrancas) shows only
		// id/value/status/externalReference/pixTransaction, no separate QR
		// identifier. api's own CreatePixQRCode stores the created object's
		// "id" as PixDeposit.ProviderQRCodeID (plan §4.2/§4.3), so resolving
		// via this same "id" is correct as long as that identifier is stable
		// across creation and the payment webhook — confirm against a live
		// Asaas-sandbox round-trip before trusting this, same posture as
		// every other "confirm before build" item in this file.
		PixQRCodeID       string `json:"id"`
		ExternalReference string `json:"externalReference"`
		// Payer is NOT confirmed present on the real payload (the fetched
		// example carries no such object) — plan §4.3/§10 Q15 already flags
		// this as open: if Asaas's payment query/webhook never exposes payer
		// CPF, the anti-fraud gate has no source at all and needs a design
		// decision, not a guessed field name. Left here as the design's
		// original placeholder; do not trust it without live verification.
		Payer struct {
			CPFCNPJ string `json:"cpfCnpj"`
			Name    string `json:"name"`
		} `json:"payer"`
	} `json:"payment"`
	// MED clawback (plan §7.3) — a DEDICATED event, never inferred from the
	// conservation-check discovering a mismatch after the fact. Resolves via
	// the top-level Account.ID field above (Asaas events carry the owning
	// account at the top level regardless of event type — unverified
	// assumption, same "confirm before build" posture as the rest of this
	// file). Field names/shape unverified against a live payload.
	Transfer struct {
		ID                string  `json:"id"`
		Value             float64 `json:"value"`
		ExternalReference string  `json:"externalReference"`
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
		if err := h.baas.ProcessAccountStatusWebhook(c.Context(), body.Account.ID, body.Account.Status); err != nil {
			return sendProblem(c, err)
		}
	case body.Event == "PAYMENT_RECEIVED" || body.Event == "PAYMENT_CONFIRMED":
		if err := h.svc.ConfirmAsaasDeposit(c.Context(), body.Payment.PixQRCodeID, body.Payment.Payer.CPFCNPJ, body.Payment.Payer.Name); err != nil {
			return sendProblem(c, err)
		}
	case body.Event == "TRANSFER_MED_CLAWBACK" || body.Event == "PIX_MED_RETURNED":
		amount := centavosFromReais(body.Transfer.Value)
		if err := h.svc.ProcessMedClawback(c.Context(), body.Account.ID, amount, body.Transfer.ExternalReference); err != nil {
			return sendProblem(c, err)
		}
	default:
		slog.WarnContext(c.Context(), "asaas webhook: unhandled event", "event", body.Event)
	}
	return c.SendStatus(fiber.StatusOK)
}
