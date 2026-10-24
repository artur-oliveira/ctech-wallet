// Package v1 wires the wallet HTTP routes onto a Fiber app.
package v1

import (
	"net/http"

	"gopkg.aoctech.app/api-commons/cache"
	"gopkg.aoctech.app/api-commons/drain"
	"gopkg.aoctech.app/api-commons/ws"
	"gopkg.aoctech.app/wallet/api/internal/awsclient"
	"gopkg.aoctech.app/wallet/api/internal/config"
	"gopkg.aoctech.app/wallet/api/internal/middleware"
	"gopkg.aoctech.app/wallet/api/internal/oauthresource"
	"gopkg.aoctech.app/wallet/api/internal/services"
	"gopkg.aoctech.app/wallet/api/internal/walletproblem"

	"github.com/gofiber/fiber/v3"
)

// handlers bundles the dependencies every route closure needs.
type handlers struct {
	svc                *services.WalletService
	userSvc            *services.UserService
	erasureSvc         *services.ErasureService
	requireDescription bool
}

// Option adjusts what Register mounts.
type Option func(*registerOptions)

type registerOptions struct {
	billingEvents *BillingEvents
	drain         *drain.Tracker
}

// WithDrain makes the /ws connections drainable at shutdown (go-common drain).
func WithDrain(t *drain.Tracker) Option {
	return func(o *registerOptions) { o.drain = t }
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
	h := &handlers{svc: svc, userSvc: userSvc, erasureSvc: erasureSvc, requireDescription: cfg.RequireDescription}
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

	limit := newUserLimits(c)

	v1 := app.Group("/v1.0")

	// Health (unauthenticated): /v1.0/health is a dependency-free liveness probe;
	// /v1.0/health-check is the detailed dependency report the ALB target group
	// probes (it accepts 200 and 207).
	RegisterHealth(v1, clients, c, verifier, cfg)
	RegisterWS(v1, verifier, wsRegistry, cfg.CorsAllowedOrigins, o.drain)

	// Caller state + terms addendum acceptance.
	a := v1.Group("/auth", auth, middleware.RequireUser)
	a.Get("/me", limit.read, middleware.RequireUserScope(middleware.ScopeWalletStateRead), h.getMe)
	a.Post("/terms-addendum/accept", limit.read, middleware.RequireUserScope(middleware.ScopeWalletTermsWrite), fresh, h.acceptTermsAddendum)

	// User routes — Bearer user JWT.
	w := v1.Group("/wallet", auth, middleware.RequireUser)
	w.Get("/", limit.read, middleware.RequireUserScope(middleware.ScopeWalletBalancesRead), h.getWallet)
	w.Post("/deposits", limit.money, middleware.RequireUserScope(middleware.ScopeWalletDepositsWrite), middleware.RequireKYC(middleware.KYCVerified), fresh, h.createDeposit)
	w.Post("/withdrawals", limit.money, middleware.RequireUserScope(middleware.ScopeWalletWithdrawalsWrite), middleware.RequireKYC(middleware.KYCVerified), middleware.RequireRecentMFA(middleware.StepUpMaxAge), fresh, h.createWithdrawal)
	// The sale of Sandbox packs and goods moved to Poker, through Billing: the writes answer 410.
	w.Post("/sandbox/purchase", retired)
	w.Post("/sandbox/purchases", retired)
	w.Get("/sandbox/purchases", limit.read, middleware.RequireUserScope(middleware.ScopeWalletLedgerRead), h.listSandboxPurchases) // old purchases stay readable
	w.Post("/sandbox/purchases/:id/refund", retired)
	w.Get("/product-purchases", limit.read, middleware.RequireUserScope(middleware.ScopeWalletLedgerRead), h.listProductPurchases)
	w.Get("/:type/ledger", limit.read, middleware.RequireUserScope(middleware.ScopeWalletLedgerRead), h.getLedger)

	// The game wallet was merged into `real` and the responsible-gambling rules (consent, limits,
	// self-exclusion) are Poker's now: these answer 410, never a 404 that looks like a typo.
	for _, route := range []struct{ method, path string }{
		{http.MethodPost, "/game/withdraw"},
		{http.MethodPost, "/game/deposit"},
		{http.MethodPost, "/gambling/self-exclude"},
		{http.MethodPost, "/gambling/self-exclude/revoke"},
		{http.MethodGet, "/gambling/limits"},
		{http.MethodPut, "/gambling/limits"},
		{http.MethodDelete, "/gambling/limits/pending"},
		{http.MethodPost, "/gambling/activate"},
	} {
		w.Add([]string{route.method}, route.path, retired)
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
	// The old game-named hold routes, the eligibility status and the one-off export left with the
	// rules (Poker owns them): 410.
	internal.Post("/wallet/game/hold", retired)
	internal.Post("/wallet/game/hold/:hold_id/release", retired)
	internal.Post("/wallet/game/cashout", retired)
	internal.Get("/wallet/game/status/:user_id", retired)
	internal.Get("/wallet/holds/status/:user_id", retired)
	internal.Get("/wallet/responsible/export", retired)
	// Holds stake `real`: the wallet reserves and settles the money, the games and their rules are
	// Poker's.
	hw := internal.Group("/wallet/holds")
	hw.Post("/", middleware.RequireScope(middleware.ScopeWalletHold), h.holdGame)
	hw.Post("/cashout", middleware.RequireScope(middleware.ScopeWalletHold), h.cashoutGame)
	hw.Post("/:hold_id/release", middleware.RequireScope(middleware.ScopeWalletHold), h.releaseHold)
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
	return walletproblem.RouteMovedToBilling().Send(c)
}
