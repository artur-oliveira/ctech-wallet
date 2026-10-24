package v1

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/api-commons/idempotency"
	"gopkg.aoctech.app/wallet/api/internal/config"
	"gopkg.aoctech.app/wallet/api/internal/middleware"
)

func idemApp(calls *int) *fiber.App {
	app := fiber.New()
	app.Post("/pay", func(c fiber.Ctx) error {
		c.Locals(middleware.ClaimsKey, &middleware.Claims{Sub: c.Get("X-User")})
		return c.Next()
	}, newMoneyIdempotency(nil, &config.Config{}), func(c fiber.Ctx) error {
		*calls++
		return c.Status(http.StatusCreated).SendString(`{"txid":"t1"}`)
	})
	return app
}

func pay(t *testing.T, app *fiber.App, user, key, body string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/pay", strings.NewReader(body))
	req.Header.Set("X-User", user)
	if key != "" {
		req.Header.Set(idempotency.HeaderKey, key)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// A money POST runs once per (user, key): the retry gets the stored answer, another body is a
// conflict, another user's same key is independent, and no key at all is refused.
func TestMoneyRoutesReplayAndRefuse(t *testing.T) {
	var calls int
	app := idemApp(&calls)

	if r := pay(t, app, "u1", "k1", `{"amount":100}`); r.StatusCode != http.StatusCreated {
		t.Fatalf("first = %d, want 201", r.StatusCode)
	}
	r := pay(t, app, "u1", "k1", `{"amount":100}`)
	if r.StatusCode != http.StatusCreated || r.Header.Get(idempotency.HeaderReplay) != "true" || calls != 1 {
		t.Fatalf("replay = %d replay=%q calls=%d, want 201 replayed without running the handler", r.StatusCode, r.Header.Get(idempotency.HeaderReplay), calls)
	}
	if r := pay(t, app, "u1", "k1", `{"amount":999}`); r.StatusCode != http.StatusConflict {
		t.Fatalf("same key, other body = %d, want 409", r.StatusCode)
	}
	if r := pay(t, app, "u2", "k1", `{"amount":100}`); r.StatusCode != http.StatusCreated || calls != 2 {
		t.Fatalf("other user same key = %d calls=%d, want an independent 201", r.StatusCode, calls)
	}
	if r := pay(t, app, "u1", "", `{"amount":100}`); r.StatusCode != http.StatusBadRequest {
		t.Fatalf("no key = %d, want 400", r.StatusCode)
	}
}
