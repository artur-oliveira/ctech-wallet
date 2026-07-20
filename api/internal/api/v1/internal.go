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
	if err := h.svc.ConfirmDeposit(c.Context(), body.Txid, body.PayerCPF, body.PayerName, false); err != nil {
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

// holdGame reserves a player's buy-in against their game wallet (M2M, scope
// internal:wallet:game-hold — e.g. ctech-poker at table-join).
func (h *handlers) holdGame(c fiber.Ctx) error {
	var body HoldRequest
	if p := bindJSON(c, &body); p != nil {
		return sendProblem(c, p)
	}
	hold, err := h.svc.HoldGame(c.Context(), body.UserID, body.Amount, body.TableRef, body.IdempotencyKey)
	if err != nil {
		return sendProblem(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(hold)
}

// releaseHold refunds a `held` hold in full (M2M, scope
// internal:wallet:game-hold — e.g. a table/hand that never played).
func (h *handlers) releaseHold(c fiber.Ctx) error {
	var body ReleaseRequest
	if p := bindJSON(c, &body); p != nil {
		return sendProblem(c, p)
	}
	hold, err := h.svc.ReleaseHold(c.Context(), body.UserID, c.Params("hold_id"), body.IdempotencyKey)
	if err != nil {
		return sendProblem(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(hold)
}

// cashoutGame credits the caller's final stack (M2M, scope
// internal:wallet:game-cashout). Amount is credited exactly as sent, never
// bounded by the sum of the listed holds — see WalletService.CashoutGame.
func (h *handlers) cashoutGame(c fiber.Ctx) error {
	var body CashoutRequest
	if p := bindJSON(c, &body); p != nil {
		return sendProblem(c, p)
	}
	entry, err := h.svc.CashoutGame(c.Context(), body.UserID, body.Amount, body.TableRef, body.HoldIDs, body.IdempotencyKey)
	if err != nil {
		return sendProblem(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(entry)
}

// gameStatus reports a user's real-money eligibility to a skill game (M2M,
// scope internal:wallet:game-status). Eligibility = activated AND not
// self-excluded AND limits configured — the caller (e.g. ctech-poker) must
// treat anything else as not-eligible for real-money buy-in.
func (h *handlers) gameStatus(c fiber.Ctx) error {
	st, err := h.svc.GameEligibilityFor(c.Context(), c.Params("user_id"))
	if err != nil {
		return sendProblem(c, err)
	}
	return c.JSON(st)
}
