package v1

import (
	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/middleware"
	"gopkg.aoctech.app/wallet/api/internal/problem"

	"github.com/gofiber/fiber/v3"
)

// getWallet returns the caller's balances: `real`, and `sandbox` when it exists. The game wallet
// was merged into `real`, and gambling consent and limits are Poker's.
func (h *handlers) getWallet(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)
	realw, gamew, sandboxw, err := h.svc.GetBalances(c.Context(), userID)
	if err != nil {
		return sendProblem(c, err)
	}
	out := walletBalancesResponse(realw, gamew, sandboxw)
	limits, err := h.svc.RailLimits(c.Context(), userID, realw)
	if err != nil {
		return sendProblem(c, err)
	}
	out["limits"] = limits
	return c.JSON(out)
}

func walletBalancesResponse(realw, gamew, sandboxw *wallet.Wallet) fiber.Map {
	out := fiber.Map{"real": realw}
	if gamew != nil {
		out["game"] = gamew
	}
	if sandboxw != nil {
		out["sandbox"] = sandboxw
	}
	return out
}

// createDeposit opens a PIX charge for the caller's real wallet.
func (h *handlers) createDeposit(c fiber.Ctx) error {
	var body DepositRequest
	if p := bindJSON(c, &body); p != nil {
		return sendProblem(c, p)
	}
	cl := middleware.GetClaims(c)
	idemKey, p := requireIdempotencyKey(c)
	if p != nil {
		return sendProblem(c, p)
	}
	dep, charge, err := h.svc.InitiateDeposit(c.Context(), cl.Sub, cl.KYCLevel, body.Amount, idemKey)
	if err != nil {
		return sendProblem(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"txid":             dep.Txid,
		"amount":           dep.AmountExpected,
		"status":           dep.Status,
		"pix_copia_e_cola": charge.QRCode,
		"qr_code_base64":   charge.QRCodeB64,
		"expires_at":       dep.TTL,
	})
}

// createWithdrawal debits amount and initiates a PIX payout to the caller's KYC CPF.
func (h *handlers) createWithdrawal(c fiber.Ctx) error {
	var body WithdrawRequest
	if p := bindJSON(c, &body); p != nil {
		return sendProblem(c, p)
	}
	idemKey, p := requireIdempotencyKey(c)
	if p != nil {
		return sendProblem(c, p)
	}
	cl := middleware.GetClaims(c)
	w, err := h.svc.Withdraw(c.Context(), cl.Sub, cl.KYCLevel, body.Amount, idemKey)
	if err != nil {
		return sendProblem(c, err)
	}
	if w == nil {
		// Defensive: Withdraw always returns a record or an error.
		return sendProblem(c, problem.InternalServer("saque indisponível no momento"))
	}
	status := fiber.StatusCreated
	if w.Status == wallet.WithdrawProcessing {
		status = fiber.StatusAccepted // payout still in flight; reconciliation will resolve
	}
	return c.Status(status).JSON(w)
}

// getLedger returns a paginated statement for one wallet type. game exists only
// after activation; sandbox may exist independently and remains readable before
// activation so a player can inspect their complete virtual-credit history.
func (h *handlers) getLedger(c fiber.Ctx) error {
	walletType := c.Params("type")
	userID := middleware.GetUserID(c)
	realw, gamew, sandboxw, err := h.svc.GetBalances(c.Context(), userID)
	if err != nil {
		return sendProblem(c, err)
	}

	var target *wallet.Wallet
	switch walletType {
	case wallet.TypeReal:
		target = realw
	case wallet.TypeGame:
		target = gamew
	case wallet.TypeSandbox:
		target = sandboxw
	default:
		return sendProblem(c, problem.BadRequest("tipo de carteira inválido"))
	}
	if target == nil {
		return sendProblem(c, problem.GamblingNotActivated())
	}
	limit := historyLimit(c)
	startKey := decodeCursor(c.Query(queryParamCursor))
	if !cursorMatchesWallet(startKey, target.WalletID) {
		return sendProblem(c, problem.BadRequest("cursor inválido"))
	}
	res, err := h.svc.Statement(c.Context(), target.WalletID, limit, startKey)
	if err != nil {
		return sendProblem(c, err)
	}
	return sendStatement(c, res)
}
