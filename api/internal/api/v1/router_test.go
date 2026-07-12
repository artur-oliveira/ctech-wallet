package v1

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/artur-oliveira/ctech-wallet/api/internal/cache"
	"github.com/artur-oliveira/ctech-wallet/api/internal/config"

	"github.com/gofiber/fiber/v3"
)

// gatedRoutes move money INTO the ring-fence. These are what GAMBLING_ENABLED gates.
var gatedRoutes = []string{
	"/v1.0/wallet/gambling/activate",
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
	Register(app, cache.NewMemoryBackend(testCacheSize), cfg, nil, nil, nil, nil, WebhookSecret(""))
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
	if !paths["/v1.0/wallet/deposits"] {
		t.Error("the flag must not remove the ordinary PIX deposit route")
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
