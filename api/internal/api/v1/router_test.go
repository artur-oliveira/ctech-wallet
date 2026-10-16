package v1

import (
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"gopkg.aoctech.app/api-commons/cache"
	"gopkg.aoctech.app/wallet/api/internal/config"

	"github.com/gofiber/fiber/v3"
)

// routerApp mounts the routes. The service dependencies are nil: these tests assert only which routes
// EXIST, and an unauthenticated request is rejected by the auth middleware long before any handler runs.
func routerApp(t *testing.T) *fiber.App {
	t.Helper()
	app := fiber.New()
	cfg := &config.Config{TablePrefix: "test"}
	Register(app, cache.NewMemoryBackend(testCacheSize), nil, cfg, nil, nil, nil, nil, nil)
	return app
}

// registeredPaths returns the POST paths the router actually mounted.
//
// We assert against the route table rather than probing with a request: every
// /wallet/* path sits behind the group's auth middleware, so an unauthenticated
// probe answers 401 whether or not the route exists. The route table is the thing
// we actually care about — whether the handler is reachable at all.
func registeredPaths(app *fiber.App) map[string]bool {
	out := map[string]bool{}
	for _, r := range app.GetRoutes() {
		if r.Method == http.MethodPost {
			out[r.Path] = true
		}
	}
	return out
}

// The PIX rail routes: deposits and withdrawals on the real wallet are always mounted.
func TestRailRoutesRegistered(t *testing.T) {
	paths := registeredPaths(routerApp(t))
	for _, path := range []string{"/v1.0/wallet/deposits", "/v1.0/wallet/withdrawals"} {
		if !paths[path] {
			t.Errorf("%s not registered", path)
		}
	}
}

// ctech-account calls {participant url}/internal/erasure/eligibility/{sub}
// with url = https://wallet-api.aoctech.app/v1.0 (account-deletion phase 3).
func TestErasureEligibilityRouteIsRegisteredAtTheContractPath(t *testing.T) {
	const path = "/v1.0/internal/erasure/eligibility/:sub"
	for _, r := range routerApp(t).GetRoutes() {
		if r.Method == http.MethodGet && r.Path == path {
			return
		}
	}
	t.Fatalf("%s is missing", path)
}

// Poker stakes real money through exactly these three routes.
func TestTheHoldRoutesAreRegistered(t *testing.T) {
	post := registeredPaths(routerApp(t))
	for _, path := range []string{
		"/v1.0/internal/wallet/holds/", "/v1.0/internal/wallet/holds/cashout", "/v1.0/internal/wallet/holds/:hold_id/release",
	} {
		if !post[path] {
			t.Errorf("POST %s is missing", path)
		}
	}
}

// The list of routes that move money is closed. A new one fails this test on purpose: add it here only
// after reading why Sandbox must never be funded from `real`. Everything else a client might still call
// is a retired route that answers 410 (listed below, and checked).
var liveMoneyRoutes = []string{
	"/v1.0/wallet/deposits",
	"/v1.0/wallet/withdrawals",
	"/v1.0/internal/wallet/sandbox/credit",
	"/v1.0/internal/wallet/sandbox/debit",
	"/v1.0/internal/wallet/real/debit",
	"/v1.0/internal/wallet/holds/",
	"/v1.0/internal/wallet/holds/cashout",
	"/v1.0/internal/wallet/holds/:hold_id/release",
	"/v1.0/auth/terms-addendum/accept",
}

var retiredRoutes = map[string][]string{
	http.MethodPost: {
		"/v1.0/wallet/sandbox/purchase", "/v1.0/wallet/sandbox/purchases", "/v1.0/wallet/sandbox/purchases/:id/refund",
		"/v1.0/wallet/game/withdraw", "/v1.0/wallet/game/deposit",
		"/v1.0/wallet/gambling/self-exclude", "/v1.0/wallet/gambling/self-exclude/revoke", "/v1.0/wallet/gambling/activate",
		"/v1.0/internal/pix/confirm-deposit", "/v1.0/internal/pix/confirm-sandbox-purchase", "/v1.0/internal/pix/confirm-product-purchase",
		"/v1.0/internal/wallet/charge",
		"/v1.0/internal/wallet/sandbox-purchase", "/v1.0/internal/wallet/sandbox-purchase/:id/refund",
		"/v1.0/internal/wallet/product-purchase", "/v1.0/internal/wallet/product-purchase/:id/refund",
		"/v1.0/internal/wallet/game/hold", "/v1.0/internal/wallet/game/hold/:hold_id/release", "/v1.0/internal/wallet/game/cashout",
	},
	http.MethodPut:    {"/v1.0/wallet/gambling/limits"},
	http.MethodDelete: {"/v1.0/wallet/gambling/limits/pending"},
	http.MethodGet: {
		"/v1.0/wallet/gambling/limits",
		"/v1.0/internal/wallet/charge/:id",
		"/v1.0/internal/wallet/sandbox-purchase/skus", "/v1.0/internal/wallet/sandbox-purchase/:id",
		"/v1.0/internal/wallet/product-purchase/skus", "/v1.0/internal/wallet/product-purchase/:id",
		"/v1.0/internal/wallet/game/status/:user_id", "/v1.0/internal/wallet/holds/status/:user_id",
		"/v1.0/internal/wallet/responsible/export",
	},
}

// TestNoRouteConvertsSandboxToAnything: the only routes that write are the closed list above (plus the
// retired ones). None of them moves money from `real` into Sandbox, and Sandbox has only the credit and
// debit an M2M client asks for. This is the successor of the ring-fence regression test.
func TestNoRouteConvertsSandboxToAnything(t *testing.T) {
	retired := map[string]bool{}
	for method, paths := range retiredRoutes {
		for _, p := range paths {
			retired[method+" "+p] = true
		}
	}
	live := map[string]bool{}
	for _, p := range liveMoneyRoutes {
		live[http.MethodPost+" "+p] = true
	}
	var unexpected []string
	for _, r := range routerApp(t).GetRoutes() {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			continue
		}
		key := r.Method + " " + r.Path
		// The groups' own middleware entries (auth, scope checks) have no handler of their own.
		groupPrefix := r.Path == "/v1.0/auth" || r.Path == "/v1.0/internal" || r.Path == "/v1.0/wallet"
		if live[key] || retired[key] || groupPrefix || strings.HasSuffix(r.Path, "/*") {
			continue
		}
		unexpected = append(unexpected, key)
	}
	sort.Strings(unexpected)
	if len(unexpected) > 0 {
		t.Fatalf("routes that write and are neither on the live list nor retired: %v", unexpected)
	}
	for key := range live {
		path := strings.TrimPrefix(key, http.MethodPost+" ")
		if strings.Contains(path, "sandbox") && path != "/v1.0/internal/wallet/sandbox/credit" && path != "/v1.0/internal/wallet/sandbox/debit" {
			t.Errorf("a live route touches Sandbox besides credit and debit: %s", path)
		}
	}
}

// A retired route answers 410 with the stable route-retired type once the caller is authenticated,
// and the normal 401 before that, so retirement does not leak which routes exist.
func TestRetiredRoutesAnswer410ToACallerAndTheNormal401ToAnonymous(t *testing.T) {
	bare := fiber.New()
	bare.Post("/x", retired)
	resp, err := bare.Test(httptest.NewRequest(http.MethodPost, "/x", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusGone {
		t.Fatalf("retired = %d, want 410", resp.StatusCode)
	}

	app := routerApp(t)
	for method, paths := range retiredRoutes {
		for _, path := range paths {
			concrete := strings.NewReplacer(":id", "x", ":hold_id", "x", ":user_id", "x").Replace(path)
			r, err := app.Test(httptest.NewRequest(method, concrete, nil))
			if err != nil {
				t.Fatal(err)
			}
			if r.StatusCode != http.StatusUnauthorized {
				t.Errorf("%s %s anonymous = %d, want 401", method, path, r.StatusCode)
			}
		}
	}
}
