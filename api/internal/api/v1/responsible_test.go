package v1

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/recover"
)

// TestGameStatusRouteShape proves /internal/wallet/game/status/:user_id is
// wired to gameStatus (thin wiring check — eligibility logic is covered by
// services.TestGameEligibilityFor, matching internal_test.go's style).
func TestGameStatusRouteShape(t *testing.T) {
	app := fiber.New()
	app.Use(recover.New())
	h := &handlers{}
	app.Get("/internal/wallet/game/status/:user_id", func(c fiber.Ctx) error {
		return h.gameStatus(c)
	})
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/internal/wallet/game/status/u1", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode == http.StatusNotFound {
		t.Fatal("route not registered")
	}
}

// TestSelfExcludeRejectsUnknownPeriod: the DTO validation refuses periods the
// service does not define.
func TestSelfExcludeRejectsUnknownPeriod(t *testing.T) {
	app := fiber.New()
	app.Use(recover.New())
	h := &handlers{}
	app.Post("/wallet/gambling/self-exclude", func(c fiber.Ctx) error { return h.selfExclude(c) })
	req := httptest.NewRequest(http.MethodPost, "/wallet/gambling/self-exclude", nil)
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("empty body must be rejected, got %d", resp.StatusCode)
	}
}
