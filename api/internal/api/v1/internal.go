package v1

import (
	"crypto/subtle"

	"github.com/artur-oliveira/ctech-wallet/api/internal/problem"

	"github.com/gofiber/fiber/v3"
)

// HeaderWebhookSecret is the shared-secret header the Inter webhook presents.
const HeaderWebhookSecret = "X-Webhook-Secret"

// pixWebhook is the Inter payment callback. It authenticates via a shared secret
// (not the account JWT) and NEVER credits from the payload — it re-queries each
// txid through the service, which is the source of truth.
func (h *handlers) pixWebhook(c fiber.Ctx) error {
	if h.webhookSecret == "" ||
		subtle.ConstantTimeCompare([]byte(c.Get(HeaderWebhookSecret)), []byte(h.webhookSecret)) != 1 {
		return sendProblem(c, problem.Unauthorized("webhook secret inválido"))
	}
	var body WebhookPayload
	if p := bindJSON(c, &body); p != nil {
		// A malformed payload is still just a wake-up; ack to stop retries only on auth,
		// but here we surface 400 so Inter resends a well-formed one.
		return sendProblem(c, p)
	}
	for _, p := range body.Pix {
		if p.Txid == "" {
			continue
		}
		if err := h.svc.ConfirmDeposit(c.Context(), p.Txid); err != nil {
			return sendProblem(c, err)
		}
	}
	return c.SendStatus(fiber.StatusOK)
}

// sandboxCredit grants sandbox currency (M2M, scope internal:wallet:credit).
func (h *handlers) sandboxCredit(c fiber.Ctx) error {
	var body SandboxOpRequest
	if p := bindJSON(c, &body); p != nil {
		return sendProblem(c, p)
	}
	entry, err := h.svc.CreditSandbox(c.Context(), body.UserID, body.Amount, body.IdempotencyKey, body.Reason)
	if err != nil {
		return sendProblem(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(entry)
}

// sandboxDebit spends sandbox currency (M2M, scope internal:wallet:debit).
func (h *handlers) sandboxDebit(c fiber.Ctx) error {
	var body SandboxOpRequest
	if p := bindJSON(c, &body); p != nil {
		return sendProblem(c, p)
	}
	entry, err := h.svc.DebitSandbox(c.Context(), body.UserID, body.Amount, body.IdempotencyKey, body.Reason)
	if err != nil {
		return sendProblem(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(entry)
}
