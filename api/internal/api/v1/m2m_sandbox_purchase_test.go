package v1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/recover"
)

func TestM2MListSandboxSKUsRouteRegistered(t *testing.T) {
	app := newSandboxPurchaseTestApp(t)

	req := httptest.NewRequest(http.MethodGet, "/internal/wallet/sandbox-purchase/skus", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var skus []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&skus); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(skus) == 0 {
		t.Fatal("expected a non-empty SKU catalog")
	}
	first := skus[0]
	for _, field := range []string{"id", "price_cents", "base_credits", "bonus_percent", "total_credits"} {
		if _, ok := first[field]; !ok {
			t.Fatalf("expected field %q in SKU response, got %+v", field, first)
		}
	}
}

func TestExpiresAtRFC3339(t *testing.T) {
	got := expiresAtRFC3339(1735689600) // 2025-01-01T00:00:00Z
	want := "2025-01-01T00:00:00Z"
	if got != want {
		t.Fatalf("expiresAtRFC3339(1735689600) = %q, want %q", got, want)
	}
}

func newSandboxPurchaseTestApp(t *testing.T) *fiber.App {
	t.Helper()
	app := fiber.New()
	app.Use(recover.New())
	h := &handlers{}
	app.Get("/internal/wallet/sandbox-purchase/skus", h.m2mListSandboxSKUs)
	return app
}
