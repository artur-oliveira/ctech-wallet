// Package v1 wires the wallet HTTP routes onto a Fiber app.
package v1

import (
	"gopkg.aoctech.app/api-commons/cache"
	"gopkg.aoctech.app/api-commons/ws"
	"gopkg.aoctech.app/wallet/api/internal/awsclient"
	"gopkg.aoctech.app/wallet/api/internal/config"
	"gopkg.aoctech.app/wallet/api/internal/middleware"
	"gopkg.aoctech.app/wallet/api/internal/pix"
	"gopkg.aoctech.app/wallet/api/internal/services"

	"github.com/gofiber/fiber/v3"
)

// handlers bundles the dependencies every route closure needs.
type handlers struct {
	svc     *services.WalletService
	userSvc *services.UserService
}

// Register mounts all wallet routes under /v1.0.
func Register(app *fiber.App, c cache.Backend, cfg *config.Config, clients *awsclient.Clients, pixClient pix.PixClient, svc *services.WalletService, userSvc *services.UserService, wsRegistry ws.Registry) {
	h := &handlers{svc: svc, userSvc: userSvc}
	verifier := middleware.NewVerifier(cfg.CtechJWKSURL, cfg.ServiceAudience, cfg.CtechURL, c)
	auth := verifier.Middleware()

	v1 := app.Group("/v1.0")

	// Health (unauthenticated): /v1.0/health is a dependency-free liveness probe;
	// /v1.0/health-check is the detailed dependency report the ALB target group
	// probes (it accepts 200 and 207).
	RegisterHealth(v1, clients, c, pixClient, verifier, cfg)
	RegisterWS(v1, verifier, wsRegistry, cfg.CorsAllowedOrigins)

	// Caller state + terms addendum acceptance.
	a := v1.Group("/auth", auth)
	a.Get("/me", h.getMe)
	a.Post("/terms-addendum/accept", h.acceptTermsAddendum)

	// User routes — Bearer user JWT.
	w := v1.Group("/wallet", auth)
	w.Get("/", h.getWallet)
	w.Post("/deposits", middleware.RequireKYC(middleware.KYCVerified), h.createDeposit)
	w.Post("/withdrawals", middleware.RequireKYC(middleware.KYCVerified), middleware.RequireRecentMFA(middleware.StepUpMaxAge), h.createWithdrawal)
	w.Post("/sandbox/purchase", h.purchaseSandbox)
	w.Get("/:type/ledger", h.getLedger)

	// Returning money OUT of the ring-fence is ALWAYS available — never behind the
	// flag. game holds real money (Invariant #9), so a route out must exist no
	// matter how the flag is set: turning GAMBLING_ENABLED off must never strand a
	// user's own money in a game wallet with no way to get it back. Reducing
	// exposure is never something we block. A user who never activated simply gets
	// 409 gambling-not-activated.
	w.Post("/game/withdraw", h.gameWithdraw)

	// Responsible gambling — ALWAYS registered, never behind the flag: self-
	// excluding and lowering limits reduce exposure (same principle as
	// game/withdraw above). The deposit door itself stays flag-gated.
	w.Post("/gambling/self-exclude", h.selfExclude)
	w.Post("/gambling/self-exclude/revoke", h.revokeSelfExclusion)
	w.Get("/gambling/limits", h.getGameLimits)
	w.Put("/gambling/limits", h.putGameLimits)
	w.Delete("/gambling/limits/pending", h.cancelPendingLimits)
	w.Post("/gambling/activate", middleware.RequireKYC(middleware.KYCVerified), h.activateGambling)

	// Everything that moves money INTO the ring-fence is flag-gated: with
	// GAMBLING_ENABLED off these routes do not exist (404). An absent route cannot
	// be reached by a bug, a stale client, or a forgotten check. The flag flips only
	// once the personal limit engine is live — a user must never reach a gambling
	// wallet with no limits configured. /sandbox/purchase stays registered above
	// because the service already refuses it for a non-activated user.
	if cfg.GamblingEnabled {
		w.Post("/game/deposit", middleware.RequireKYC(middleware.KYCVerified), h.gameDeposit)
	}

	// Internal routes — all M2M client_credentials + scope, gated after auth.
	internal := v1.Group("/internal", auth)
	// pix-gateway's webhook Lambda, after it has already re-queried Inter.
	internal.Post("/pix/confirm-deposit", middleware.RequireScope(middleware.ScopePixConfirmDeposit), h.confirmDeposit)
	sb := internal.Group("/wallet/sandbox")
	sb.Post("/credit", middleware.RequireScope(middleware.ScopeWalletCredit), h.sandboxCredit)
	sb.Post("/debit", middleware.RequireScope(middleware.ScopeWalletDebit), h.sandboxDebit)
	rw := internal.Group("/wallet/real")
	rw.Post("/debit", middleware.RequireScope(middleware.ScopeWalletRealDebit), h.realDebit)
	// game wallet holds (skill-game integration, e.g. ctech-poker real-money mode).
	gw := internal.Group("/wallet/game")
	gw.Post("/hold", middleware.RequireScope(middleware.ScopeWalletGameHold), h.holdGame)
	gw.Post("/hold/:hold_id/release", middleware.RequireScope(middleware.ScopeWalletGameHold), h.releaseHold)
	gw.Post("/cashout", middleware.RequireScope(middleware.ScopeWalletGameCashout), h.cashoutGame)
	// Real-money eligibility for skill games (ctech-poker Phase 5). Registered
	// unconditionally: poker must see "not eligible" even while the flag is off.
	gw.Get("/status/:user_id", middleware.RequireScope(middleware.ScopeWalletGameStatus), h.gameStatus)
}
