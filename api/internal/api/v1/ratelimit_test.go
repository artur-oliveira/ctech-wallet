package v1

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/api-commons/cache"
	"gopkg.aoctech.app/api-commons/ratelimit"
	"gopkg.aoctech.app/wallet/api/internal/middleware"
)

type brokenCounter struct{}

func (brokenCounter) Incr(context.Context, string, time.Duration) (int64, error) {
	return 0, errors.New("valkey down")
}
func (brokenCounter) Count(context.Context, string) (int64, error) {
	return 0, errors.New("valkey down")
}

func limitedApp(h fiber.Handler) *fiber.App {
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		cl := &middleware.Claims{}
		cl.Sub = c.Get("X-Test-User")
		c.Locals(middleware.ClaimsKey, cl)
		return c.Next()
	})
	app.Get("/x", h, func(c fiber.Ctx) error { return c.SendStatus(http.StatusOK) })
	return app
}

func hit(t *testing.T, app *fiber.App, user string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("X-Test-User", user)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestUserPolicyDeniesWith429AndRetryAfterPerUser(t *testing.T) {
	app := limitedApp(userPolicy(ratelimit.NewMemoryCounter(), "t", 2, ratelimit.FailOpen))
	for range 2 {
		if r := hit(t, app, "u1"); r.StatusCode != http.StatusOK {
			t.Fatalf("within budget: %d", r.StatusCode)
		}
	}
	r := hit(t, app, "u1")
	if r.StatusCode != http.StatusTooManyRequests || r.Header.Get("Retry-After") == "" ||
		r.Header.Get("Content-Type") != "application/problem+json" {
		t.Fatalf("over budget: %d retry-after=%q ct=%q", r.StatusCode, r.Header.Get("Retry-After"), r.Header.Get("Content-Type"))
	}
	if r := hit(t, app, "u2"); r.StatusCode != http.StatusOK {
		t.Fatalf("another user must not share the bucket: %d", r.StatusCode)
	}
}

func TestMoneyPolicyFailsClosedAndReadPolicyFailsOpenWhenTheCounterIsDown(t *testing.T) {
	money := limitedApp(userPolicy(brokenCounter{}, "m", 1, ratelimit.FailClosed))
	if r := hit(t, money, "u1"); r.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("money route with limiter down: %d, want 503", r.StatusCode)
	}
	read := limitedApp(userPolicy(brokenCounter{}, "r", 1, ratelimit.FailOpen))
	if r := hit(t, read, "u1"); r.StatusCode != http.StatusOK {
		t.Fatalf("read route with limiter down: %d, want 200", r.StatusCode)
	}
}

func TestMoneyAndReadRoutesCarryTheirPolicy(t *testing.T) {
	l := newUserLimits(cache.NewMemoryBackend(testCacheSize))
	if l.read == nil || l.money == nil {
		t.Fatal("limits not built")
	}
}
