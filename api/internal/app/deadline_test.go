package app

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/wallet/api/internal/config"
)

func slowApp(t *testing.T, timeout time.Duration) *fiber.App {
	t.Helper()
	app := newFiberApp(&config.Config{RequestTimeout: timeout, CorsAllowedOrigins: []string{"https://wallet.example"}})
	wait := func(c fiber.Ctx) error {
		select {
		case <-c.Context().Done():
			return c.Context().Err()
		case <-time.After(300 * time.Millisecond):
			return c.SendString("finished")
		}
	}
	app.Get("/v1.0/wallet/slow", wait)
	app.Post("/v1.0/internal/billing/events", wait)
	return app
}

func call(t *testing.T, app *fiber.App, method, path string) (int, string) {
	t.Helper()
	res, err := app.Test(httptest.NewRequest(method, path, nil), fiber.TestConfig{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(body)
}

// A call that outlives the request budget ends with it and answers a retryable 504 problem.
func TestARouteIsBoundedByTheRequestTimeout(t *testing.T) {
	code, body := call(t, slowApp(t, 40*time.Millisecond), "GET", "/v1.0/wallet/slow")
	if code != fiber.StatusGatewayTimeout || !strings.Contains(body, "/problems/timeout") {
		t.Fatalf("status %d body %s, want a 504 problem of type /problems/timeout", code, body)
	}
}

// Billing's signed invoice.paid confirms a deposit: not cut by a request budget.
func TestBillingEventsAreNotBounded(t *testing.T) {
	if code, _ := call(t, slowApp(t, 40*time.Millisecond), "POST", "/v1.0/internal/billing/events"); code != 200 {
		t.Fatalf("status %d, want 200", code)
	}
}

// Zero turns it off.
func TestZeroRequestTimeoutDisablesIt(t *testing.T) {
	if code, _ := call(t, slowApp(t, 0), "GET", "/v1.0/wallet/slow"); code != 200 {
		t.Fatalf("status %d, want 200", code)
	}
}
