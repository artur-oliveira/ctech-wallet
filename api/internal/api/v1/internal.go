package v1

import (
	"github.com/gofiber/fiber/v3"
)

// confirmDeposit is called by pix-gateway's webhook Lambda after it has
// already re-queried the charge at Inter (M2M, scope
// internal:pix:confirm-deposit — never the account JWT). It never trusts its
// own caller either: ConfirmDeposit re-queries Inter itself through
// LambdaPixClient before crediting anything (Financial Safety Invariant 11).
func (h *handlers) confirmDeposit(c fiber.Ctx) error {
	var body ConfirmDepositRequest
	if p := bindJSON(c, &body); p != nil {
		return sendProblem(c, p)
	}
	if err := h.svc.ConfirmDeposit(c.Context(), body.Txid, body.PayerCPF, body.PayerName); err != nil {
		return sendProblem(c, err)
	}
	return c.SendStatus(fiber.StatusOK)
}

// sandboxCredit grants sandbox currency (M2M, scope internal:wallet:credit).
func (h *handlers) sandboxCredit(c fiber.Ctx) error {
	var body MovementOpRequest
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
	var body MovementOpRequest
	if p := bindJSON(c, &body); p != nil {
		return sendProblem(c, p)
	}
	entry, err := h.svc.DebitSandbox(c.Context(), body.UserID, body.Amount, body.IdempotencyKey, body.Reason)
	if err != nil {
		return sendProblem(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(entry)
}

// realDebit debits the real wallet (M2M, scope internal:wallet:real:debit —
// deliberately separate from sandbox's internal:wallet:debit, e.g. ctech-billing
// charging a subscription). No PIX leg.
func (h *handlers) realDebit(c fiber.Ctx) error {
	var body MovementOpRequest
	if p := bindJSON(c, &body); p != nil {
		return sendProblem(c, p)
	}
	entry, err := h.svc.DebitReal(c.Context(), body.UserID, body.Amount, body.IdempotencyKey, body.Reason)
	if err != nil {
		return sendProblem(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(entry)
}
