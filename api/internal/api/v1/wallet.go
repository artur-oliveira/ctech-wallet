package v1

import (
	"context"

	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/middleware"
	"gopkg.aoctech.app/wallet/api/internal/problem"

	"github.com/gofiber/fiber/v3"
)

// getWallet returns the caller's balances. The presence of game alone is the
// activation signal. An independently-created sandbox wallet is returned before
// activation so its immutable transaction history remains readable; that must
// never be interpreted as gambling consent or used to expose game actions.
func (h *handlers) getWallet(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)
	realw, gamew, sandboxw, err := h.svc.GetBalances(c.Context(), userID)
	if err != nil {
		return sendProblem(c, err)
	}
	return c.JSON(walletBalancesResponse(realw, gamew, sandboxw))
}

func walletBalancesResponse(realw, gamew, sandboxw *wallet.Wallet) fiber.Map {
	out := fiber.Map{"real": realw, "activated": gamew != nil}
	if gamew != nil {
		out["game"] = gamew
	}
	if sandboxw != nil {
		out["sandbox"] = sandboxw
	}
	return out
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
	game, sandbox, err := h.svc.ActivateGambling(c.Context(), cl.Sub, cl.KYCLevel, ip, ua, body.DailyLimit, body.WeeklyLimit, body.MonthlyLimit)
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
	res, err := h.svc.Statement(c.Context(), target.WalletID, limit, startKey)
	if err != nil {
		return sendProblem(c, err)
	}
	return sendStatement(c, res)
}

// selfExclude records a self-exclusion (30d / 90d / indefinite). Registered
// outside the gambling flag: reducing exposure is never blocked.
func (h *handlers) selfExclude(c fiber.Ctx) error {
	var body SelfExcludeRequest
	if p := bindJSON(c, &body); p != nil {
		return sendProblem(c, p)
	}
	cl := middleware.GetClaims(c)
	ex, err := h.svc.SelfExclude(c.Context(), cl.Sub, body.Period, c.IP(), string(c.RequestCtx().UserAgent()))
	if err != nil {
		return sendProblem(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(ex)
}

// revokeSelfExclusion lifts an indefinite exclusion after its 90-day floor.
func (h *handlers) revokeSelfExclusion(c fiber.Ctx) error {
	cl := middleware.GetClaims(c)
	if err := h.svc.RevokeSelfExclusion(c.Context(), cl.Sub, c.IP(), string(c.RequestCtx().UserAgent())); err != nil {
		return sendProblem(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// getGameLimits returns limits, current-window usage and exclusion state.
func (h *handlers) getGameLimits(c fiber.Ctx) error {
	st, err := h.svc.GameLimitsStatus(c.Context(), middleware.GetClaims(c).Sub)
	if err != nil {
		return sendProblem(c, err)
	}
	return c.JSON(st)
}

// putGameLimits sets the three personal deposit limits. Decreases apply
// immediately; increases wait out their cooldown as a pending set.
func (h *handlers) putGameLimits(c fiber.Ctx) error {
	var body GameLimitsRequest
	if p := bindJSON(c, &body); p != nil {
		return sendProblem(c, p)
	}
	cl := middleware.GetClaims(c)
	lim, err := h.svc.SetGameLimits(c.Context(), cl.Sub, body.DailyLimit, body.WeeklyLimit, body.MonthlyLimit,
		c.IP(), string(c.RequestCtx().UserAgent()))
	if err != nil {
		return sendProblem(c, err)
	}
	return c.JSON(lim)
}

// cancelPendingLimits drops a scheduled increase, keeping the stricter
// current limits.
func (h *handlers) cancelPendingLimits(c fiber.Ctx) error {
	lim, err := h.svc.CancelPendingLimits(c.Context(), middleware.GetClaims(c).Sub)
	if err != nil {
		return sendProblem(c, err)
	}
	return c.JSON(lim)
}
