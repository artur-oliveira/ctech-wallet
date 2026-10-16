// Package v1 wires the wallet HTTP routes onto a Fiber app.
package v1

import (
	"gopkg.aoctech.app/api-commons/cache"
	"gopkg.aoctech.app/api-commons/ws"
	"gopkg.aoctech.app/wallet/api/internal/awsclient"
	"gopkg.aoctech.app/wallet/api/internal/config"
	"gopkg.aoctech.app/wallet/api/internal/middleware"
	"gopkg.aoctech.app/wallet/api/internal/oauthresource"
	"gopkg.aoctech.app/wallet/api/internal/problem"
	"gopkg.aoctech.app/wallet/api/internal/services"

	"github.com/gofiber/fiber/v3"
)

// handlers bundles the dependencies every route closure needs.
type handlers struct {
	svc                *services.WalletService
	userSvc            *services.UserService
	erasureSvc         *services.ErasureService
	requireDescription bool
	exporter           ResponsibleExporter
}

// Option adjusts what Register mounts.
type Option func(*registerOptions)

type registerOptions struct {
	billingEvents     *BillingEvents
	responsibleExport ResponsibleExporter
}

// WithBillingEvents mounts Billing's signed invoice.paid route. It is an option
// of Register, not a call after it, because of how Fiber orders middleware: the
// /internal group below has the JWT check on its prefix, and a route added after
// the group sits behind that check, so Billing (which sends an HMAC, not a token)
// would be answered 401 before the handler ran.
func WithBillingEvents(e *BillingEvents) Option {
	return func(o *registerOptions) { o.billingEvents = e }
}

// Register mounts all wallet routes under /v1.0. revocation is the shared
// Valkey DB 0 holding ctech-account's JWT revocation list (nil in route tests).
func Register(app *fiber.App, c cache.Backend, revocation cache.Backend, cfg *config.Config, clients *awsclient.Clients, svc *services.WalletService, userSvc *services.UserService, erasureSvc *services.ErasureService, wsRegistry ws.Registry, opts ...Option) {
	var o registerOptions
	for _, opt := range opts {
		opt(&o)
	}
	h := &handlers{svc: svc, userSvc: userSvc, erasureSvc: erasureSvc, requireDescription: cfg.RequireDescription, exporter: o.responsibleExport}
	verifier := middleware.NewVerifier(cfg.CtechJWKSURL, cfg.ServiceAudience, cfg.CtechIssuerURL, c)
	if revocation != nil {
		// Fail-open on every route (D3); money routes re-check fail-closed via fresh.
		verifier.WithRevocation(revocation)
	}
	auth := verifier.Middleware()
	// fresh: user routes that change money or consent refuse a locked user's
	// token even when Valkey is down (saga protocol §5).
	fresh := middleware.RequireUnrevoked(revocation)
	oauthresource.Register(app, cfg.ServiceAudience, cfg.CtechIssuerURL)

	v1 := app.Group("/v1.0")

	// Health (unauthenticated): /v1.0/health is a dependency-free liveness probe;
	// /v1.0/health-check is the detailed dependency report the ALB target group
	// probes (it accepts 200 and 207).
	RegisterHealth(v1, clients, c, verifier, cfg)
	RegisterWS(v1, verifier, wsRegistry, cfg.CorsAllowedOrigins)

	// Caller state + terms addendum acceptance.
	a := v1.Group("/auth", auth, middleware.RequireUser)
	a.Get("/me", middleware.RequireUserScope(middleware.ScopeWalletStateRead), h.getMe)
	a.Post("/terms-addendum/accept", middleware.RequireUserScope(middleware.ScopeWalletTermsWrite), fresh, h.acceptTermsAddendum)

	// User routes — Bearer user JWT.
	w := v1.Group("/wallet", auth, middleware.RequireUser)
	w.Get("/", middleware.RequireUserScope(middleware.ScopeWalletBalancesRead), h.getWallet)
	w.Post("/deposits", middleware.RequireUserScope(middleware.ScopeWalletDepositsWrite), middleware.RequireKYC(middleware.KYCVerified), fresh, h.createDeposit)
	w.Post("/withdrawals", middleware.RequireUserScope(middleware.ScopeWalletWithdrawalsWrite), middleware.RequireKYC(middleware.KYCVerified), middleware.RequireRecentMFA(middleware.StepUpMaxAge), fresh, h.createWithdrawal)
	// The sale of Sandbox packs and goods moved to Poker, through Billing: the writes answer 410.
	w.Post("/sandbox/purchase", retired)
	w.Post("/sandbox/purchases", retired)
	w.Get("/sandbox/purchases", middleware.RequireUserScope(middleware.ScopeWalletLedgerRead), h.listSandboxPurchases) // old purchases stay readable
	w.Post("/sandbox/purchases/:id/refund", retired)
	w.Get("/product-purchases", middleware.RequireUserScope(middleware.ScopeWalletLedgerRead), h.listProductPurchases)
	w.Get("/:type/ledger", middleware.RequireUserScope(middleware.ScopeWalletLedgerRead), h.getLedger)

	// Returning money OUT of the ring-fence is ALWAYS available — never behind the
	// flag. game holds real money (Invariant #9), so a route out must exist no
	// matter how the flag is set: turning GAMBLING_ENABLED off must never strand a
	// user's own money in a game wallet with no way to get it back. Reducing
	// exposure is never something we block. A user who never activated simply gets
	// 409 gambling-not-activated.
	w.Post("/game/withdraw", middleware.RequireUserScope(middleware.ScopeWalletGameWrite), fresh, h.gameWithdraw)

	// Responsible gambling — ALWAYS registered, never behind the flag: self-
	// excluding and lowering limits reduce exposure (same principle as
	// game/withdraw above). The deposit door itself stays flag-gated.
	w.Post("/gambling/self-exclude", middleware.RequireUserScope(middleware.ScopeWalletGamblingWrite), fresh, h.selfExclude)
	w.Post("/gambling/self-exclude/revoke", middleware.RequireUserScope(middleware.ScopeWalletGamblingWrite), fresh, h.revokeSelfExclusion)
	w.Get("/gambling/limits", middleware.RequireUserScope(middleware.ScopeWalletGamblingRead), h.getGameLimits)
	w.Put("/gambling/limits", middleware.RequireUserScope(middleware.ScopeWalletGamblingWrite), fresh, h.putGameLimits)
	w.Delete("/gambling/limits/pending", middleware.RequireUserScope(middleware.ScopeWalletGamblingWrite), fresh, h.cancelPendingLimits)
	w.Post("/gambling/activate", middleware.RequireUserScope(middleware.ScopeWalletGamblingWrite), middleware.RequireKYC(middleware.KYCBasic), fresh, h.activateGambling)

	// Everything that moves money INTO the ring-fence is flag-gated: with
	// GAMBLING_ENABLED off these routes do not exist (404). An absent route cannot
	// be reached by a bug, a stale client, or a forgotten check. The flag flips only
	// once the personal limit engine is live — a user must never reach a gambling
	// wallet with no limits configured. /sandbox/purchase stays registered above
	// because the service already refuses it for a non-activated user.
	if cfg.GamblingEnabled {
		w.Post("/game/deposit", middleware.RequireUserScope(middleware.ScopeWalletGameWrite), middleware.RequireKYC(middleware.KYCVerified), fresh, h.gameDeposit)
	}

	// Internal routes — all M2M client_credentials + scope, gated after auth.
	if o.billingEvents != nil {
		RegisterBillingEvents(v1, o.billingEvents) // before the group that puts the JWT check on /internal
	}
	internal := v1.Group("/internal", auth)
	// Account-deletion saga pre-check, called by ctech-account with a token it
	// mints itself (docs/plans/2026-10-07-account-deletion-participant.md).
	internal.Get("/erasure/eligibility/:sub", middleware.RequireScope(middleware.ScopeWalletErasureEligibility), h.erasureEligibility)
	// The Pix gateway and the charge, purchase and SKU routes are gone: Billing owns the Inter
	// connection and Poker sells through it. They answer 410, not a 404 that looks like a typo.
	for _, path := range []string{"/pix/confirm-deposit", "/pix/confirm-sandbox-purchase", "/pix/confirm-product-purchase"} {
		internal.Post(path, retired)
	}
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
	// The same holds under names that do not say "game": the wallet only reserves
	// and settles money on `real` now, the games are Poker's. Same handlers.
	hw := internal.Group("/wallet/holds")
	hw.Post("/", middleware.RequireScope(middleware.ScopeWalletHold), h.holdGame)
	hw.Post("/cashout", middleware.RequireScope(middleware.ScopeWalletHold), h.cashoutGame)
	hw.Get("/status/:user_id", middleware.RequireScope(middleware.ScopeWalletHold), h.gameStatus)
	hw.Post("/:hold_id/release", middleware.RequireScope(middleware.ScopeWalletHold), h.releaseHold)
	if o.responsibleExport != nil {
		internal.Get("/wallet/responsible/export", middleware.RequireScope(middleware.ScopeWalletResponsibleExport), h.responsibleExport)
	}
	// Balance read for skill games (ctech-poker). Read-only, game+sandbox only.
	bg := internal.Group("/wallet/balance")
	bg.Get("/:user_id", middleware.RequireScope(middleware.ScopeWalletBalance), h.walletBalance)
	for _, group := range []string{"/wallet/sandbox-purchase", "/wallet/product-purchase"} {
		internal.Get(group+"/skus", retired)
		internal.Post(group, retired)
		internal.Get(group+"/:id", retired)
		internal.Post(group+"/:id/refund", retired)
	}
	internal.Post("/wallet/charge", retired)
	internal.Get("/wallet/charge/:id", retired)
}

// retired answers a route that no longer exists with a stable 410, after the normal authentication,
// so a client that still calls it learns why instead of reading a 404 as a typo.
func retired(c fiber.Ctx) error {
	return problem.RouteMovedToBilling().Send(c)
}
