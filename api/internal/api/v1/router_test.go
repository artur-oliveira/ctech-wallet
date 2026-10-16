package v1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"gopkg.aoctech.app/api-commons/cache"
	"gopkg.aoctech.app/wallet/api/internal/config"
	"gopkg.aoctech.app/wallet/api/internal/repositories"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/gofiber/fiber/v3"
)

// gatedRoutes move money INTO the ring-fence. These are what GAMBLING_ENABLED gates.
var gatedRoutes = []string{
	"/v1.0/wallet/game/deposit",
}

// returnRoute moves money OUT of the ring-fence. It is never gated.
const returnRoute = "/v1.0/wallet/game/withdraw"

// routerApp mounts the routes with the given flag. The service dependencies are
// nil: these tests assert only which routes EXIST, and an unauthenticated request
// is rejected by the auth middleware long before any handler runs.
func routerApp(t *testing.T, gamblingEnabled bool) *fiber.App {
	t.Helper()
	app := fiber.New()
	cfg := &config.Config{
		TablePrefix:     "test",
		GamblingEnabled: gamblingEnabled,
	}
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

// With the flag off, nothing that puts money INTO the ring-fence exists. That
// absence is what makes it structurally impossible to reach a gambling wallet
// before the personal limit engine ships — not a check somebody could forget.
func TestGatedRoutesAreNotRegisteredWhenFlagDisabled(t *testing.T) {
	paths := registeredPaths(routerApp(t, false))

	for _, path := range gatedRoutes {
		if paths[path] {
			t.Errorf("%s is registered with GAMBLING_ENABLED=false — it must not exist", path)
		}
	}
	// The rest of the wallet is unaffected by the flag.
	if !paths["/v1.0/wallet/sandbox/purchases"] {
		t.Error("the flag must not remove the ordinary sandbox purchase route")
	}
}

func TestGatedRoutesAreRegisteredWhenFlagEnabled(t *testing.T) {
	paths := registeredPaths(routerApp(t, true))

	for _, path := range gatedRoutes {
		if !paths[path] {
			t.Errorf("%s is missing with GAMBLING_ENABLED=true", path)
		}
	}
}

// The way OUT of the ring-fence is available whatever the flag says.
//
// game holds REAL money (Invariant #9). If this route could be switched off, then
// flipping GAMBLING_ENABLED to false would strand a user's own money in a game
// wallet with no way to get it back — money in limbo, by configuration. Reducing
// exposure is never something we block.
func TestReturnRouteIsNeverGated(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		if !registeredPaths(routerApp(t, enabled))[returnRoute] {
			t.Errorf("%s is missing with GAMBLING_ENABLED=%v — money must never be trapped in the ring-fence",
				returnRoute, enabled)
		}
	}
}

// Every ring-fence route is authenticated, gated or not.
func TestRingFenceRoutesRequireAuth(t *testing.T) {
	app := routerApp(t, true)

	for _, path := range append(append([]string{}, gatedRoutes...), returnRoute) {
		res, err := app.Test(httptest.NewRequest(http.MethodPost, path, nil))
		if err != nil {
			t.Fatalf("app.Test(%s): %v", path, err)
		}
		if res.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s unauthenticated = %d, want 401", path, res.StatusCode)
		}
	}
}

func TestProductPurchaseConfirmationRouteIsRegistered(t *testing.T) {
	const path = "/v1.0/internal/pix/confirm-product-purchase"
	if !registeredPaths(routerApp(t, false))[path] {
		t.Fatalf("%s is missing", path)
	}
}

// The caller-supplied-amount charge lives at exactly this path, because
// ctech-billing's client is already written against it
// (ctech-billing/api/internal/wallet/client.go). A rename here is a broken
// consumer, not a refactor.
func TestChargeRouteIsRegisteredAtTheContractPath(t *testing.T) {
	const path = "/v1.0/internal/wallet/charge"
	if !registeredPaths(routerApp(t, false))[path] {
		t.Fatalf("%s is missing", path)
	}
}

// The PIX rail routes are not behind the gambling flag: deposits and
// withdrawals on the real wallet are always mounted.
func TestRailRoutesRegistered(t *testing.T) {
	for _, gambling := range []bool{false, true} {
		paths := registeredPaths(routerApp(t, gambling))
		for _, path := range []string{"/v1.0/wallet/deposits", "/v1.0/wallet/withdrawals"} {
			if !paths[path] {
				t.Errorf("%s not registered (gambling=%v)", path, gambling)
			}
		}
	}
}

// ctech-account calls {participant url}/internal/erasure/eligibility/{sub}
// with url = https://wallet-api.aoctech.app/v1.0 (account-deletion phase 3).
func TestErasureEligibilityRouteIsRegisteredAtTheContractPath(t *testing.T) {
	const path = "/v1.0/internal/erasure/eligibility/:sub"
	for _, r := range routerApp(t, false).GetRoutes() {
		if r.Method == http.MethodGet && r.Path == path {
			return
		}
	}
	t.Fatalf("%s is missing", path)
}

// The holds are also reachable under names without "game": Poker moves to them
// while the old paths stay until the cleanup. Both families are one handler each.
func TestGenericHoldRoutesAreRegisteredNextToTheGameNamedOnes(t *testing.T) {
	app := routerApp(t, false)
	post := registeredPaths(app)
	for _, path := range []string{
		"/v1.0/internal/wallet/holds/", "/v1.0/internal/wallet/holds/cashout", "/v1.0/internal/wallet/holds/:hold_id/release",
		"/v1.0/internal/wallet/game/hold", "/v1.0/internal/wallet/game/cashout",
	} {
		if !post[path] {
			t.Errorf("POST %s is missing", path)
		}
	}
	found := false
	for _, r := range app.GetRoutes() {
		found = found || (r.Method == http.MethodGet && r.Path == "/v1.0/internal/wallet/holds/status/:user_id")
	}
	if !found {
		t.Error("GET /v1.0/internal/wallet/holds/status/:user_id is missing")
	}
}

type fakeExporter struct{}

func (fakeExporter) ExportPage(context.Context, map[string]types.AttributeValue, int) ([]repositories.ExportedUser, []repositories.ExportedCarriedExclusion, map[string]types.AttributeValue, error) {
	return nil, nil, nil, nil
}

func TestResponsibleExportExistsOnlyWhenMountedAndNeedsAToken(t *testing.T) {
	const path = "/v1.0/internal/wallet/responsible/export"
	has := func(app *fiber.App) bool {
		for _, r := range app.GetRoutes() {
			if r.Method == http.MethodGet && r.Path == path {
				return true
			}
		}
		return false
	}
	if has(routerApp(t, false)) {
		t.Fatalf("%s is mounted without the option", path)
	}
	app := fiber.New()
	Register(app, cache.NewMemoryBackend(testCacheSize), nil, &config.Config{TablePrefix: "test"}, nil, nil, nil, nil, nil, WithResponsibleExport(fakeExporter{}))
	if !has(app) {
		t.Fatalf("%s is missing", path)
	}
	res, err := app.Test(httptest.NewRequest(http.MethodGet, path, nil))
	if err != nil || res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated export = %v, %v (want 401)", res, err)
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

	app := routerApp(t, false)
	for _, path := range []string{
		"/v1.0/wallet/sandbox/purchase",
		"/v1.0/wallet/sandbox/purchases",
		"/v1.0/internal/wallet/charge",
		"/v1.0/internal/wallet/sandbox-purchase",
		"/v1.0/internal/wallet/product-purchase/skus",
		"/v1.0/internal/pix/confirm-deposit",
	} {
		method := http.MethodPost
		if path == "/v1.0/internal/wallet/product-purchase/skus" {
			method = http.MethodGet
		}
		r, err := app.Test(httptest.NewRequest(method, path, nil))
		if err != nil {
			t.Fatal(err)
		}
		if r.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s anonymous = %d, want 401", method, path, r.StatusCode)
		}
	}
}
