package v1

import (
	"context"

	"github.com/artur-oliveira/ctech-wallet/api/internal/domain/wallet"
	"github.com/artur-oliveira/ctech-wallet/api/internal/middleware"
	"github.com/artur-oliveira/ctech-wallet/api/internal/problem"

	"github.com/gofiber/fiber/v3"
)

// getWallet returns the caller's balances. game and sandbox are omitted entirely
// until the user activates gambling — the frontend reads their absence to decide
// whether to show any gambling surface at all, so a subscriptions-only user never
// sees one.
func (h *handlers) getWallet(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)
	realw, gamew, sandboxw, err := h.svc.GetBalances(c.Context(), userID)
	if err != nil {
		return sendProblem(c, err)
	}
	out := fiber.Map{"real": realw, "activated": gamew != nil}
	if gamew != nil {
		out["game"] = gamew
		out["sandbox"] = sandboxw
	}
	return c.JSON(out)
}

// createDeposit opens a PIX charge for the caller's real wallet.
func (h *handlers) createDeposit(c fiber.Ctx) error {
	var body DepositRequest
	if p := bindJSON(c, &body); p != nil {
		return sendProblem(c, p)
	}
	cl := middleware.GetClaims(c)
	dep, charge, err := h.svc.InitiateDeposit(c.Context(), cl.Sub, cl.KYCLevel, body.Amount)
	if err != nil {
		return sendProblem(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"txid":             dep.Txid,
		"amount":           dep.AmountExpected,
		"status":           dep.Status,
		"pix_copia_e_cola": charge.QRCode,
		"qr_code_base64":   charge.QRCodeB64,
	})
}

// createWithdrawal debits amount+fee and initiates a PIX payout.
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
	w, err := h.svc.Withdraw(c.Context(), cl.Sub, cl.KYCLevel, body.Amount, body.PixKey, idemKey)
	if err != nil {
		return sendProblem(c, err)
	}
	status := fiber.StatusCreated
	if w.Status == wallet.WithdrawProcessing {
		status = fiber.StatusAccepted // payout still in flight; reconciliation will resolve
	}
	return c.Status(status).JSON(w)
}

// purchaseSandbox debits game and credits sandbox atomically. The source is the
// game wallet, never real — see PurchaseSandbox.
func (h *handlers) purchaseSandbox(c fiber.Ctx) error {
	var body SandboxPurchaseRequest
	if p := bindJSON(c, &body); p != nil {
		return sendProblem(c, p)
	}
	return h.walletTransfer(c, h.svc.PurchaseSandbox, body.Amount)
}

// activateGambling opts the caller into the game + sandbox wallets. It records the
// addendum acceptance and then activates, both audited.
func (h *handlers) activateGambling(c fiber.Ctx) error {
	var body ActivateGamblingRequest
	if p := bindJSON(c, &body); p != nil {
		return sendProblem(c, p)
	}
	cl := middleware.GetClaims(c)
	ip, ua := c.IP(), string(c.RequestCtx().UserAgent())

	if err := h.userSvc.AcceptGamblingAddendum(c.Context(), cl.Sub, ip, ua); err != nil {
		return sendProblem(c, err)
	}
	game, sandbox, err := h.svc.ActivateGambling(c.Context(), cl.Sub, cl.KYCLevel, ip, ua)
	if err != nil {
		return sendProblem(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"game": game, "sandbox": sandbox})
}

// gameDeposit moves real money into the ring-fence (real → game). This is the edge
// the personal limit engine meters.
func (h *handlers) gameDeposit(c fiber.Ctx) error {
	var body GameTransferRequest
	if p := bindJSON(c, &body); p != nil {
		return sendProblem(c, p)
	}
	return h.walletTransfer(c, h.svc.FundGame, body.Amount)
}

// gameWithdraw moves money back out of the ring-fence (game → real). Never limited
// and never charged — this is not a PIX payout, just an internal transfer.
func (h *handlers) gameWithdraw(c fiber.Ctx) error {
	var body GameTransferRequest
	if p := bindJSON(c, &body); p != nil {
		return sendProblem(c, p)
	}
	return h.walletTransfer(c, h.svc.ReturnFromGame, body.Amount)
}

// transferOp is any service call moving money between two of the caller's wallets.
type transferOp func(ctx context.Context, userID string, amount int64, idemKey string) (*wallet.LedgerEntry, *wallet.LedgerEntry, error)

// walletTransfer is the shared body of every internal transfer route: same
// idempotency key, same response shape — only the service call differs.
func (h *handlers) walletTransfer(c fiber.Ctx, op transferOp, amount int64) error {
	idemKey, p := requireIdempotencyKey(c)
	if p != nil {
		return sendProblem(c, p)
	}
	debit, credit, err := op(c.Context(), middleware.GetUserID(c), amount, idemKey)
	if err != nil {
		return sendProblem(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"debit": debit, "credit": credit})
}

// getLedger returns a paginated statement for one wallet type. The game and
// sandbox statements exist only once the user has activated gambling.
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
	limit := intQuery(c, "limit", 50)
	if limit > 200 {
		limit = 200 // cap page size so a client cannot force a large scan
	}
	startKey := decodeCursor(c.Query("cursor"))
	res, err := h.svc.Statement(c.Context(), target.WalletID, limit, startKey)
	if err != nil {
		return sendProblem(c, err)
	}
	return sendStatement(c, res)
}
