package v1

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/recover"
)

func TestCheckDescription(t *testing.T) {
	on := &handlers{requireDescription: true}
	off := &handlers{requireDescription: false}

	cases := []struct {
		name string
		h    *handlers
		desc string
		bad  bool
	}{
		{"on, empty", on, "", true},
		{"on, spaces only", on, "   ", true},
		{"on, too short", on, "ab", true},
		{"on, ok", on, "Mesa #abc", false},
		{"on, padded ok", on, "  Mesa #abc  ", false},
		{"off, empty", off, "", false},
		{"off, spaces", off, "   ", false},
	}
	for _, tc := range cases {
		if got := tc.h.checkDescription(tc.desc); (got != nil) != tc.bad {
			t.Errorf("%s: problem=%v want bad=%v", tc.name, got, tc.bad)
		}
	}
}

// descriptionRoutes are every M2M money route that must carry a description.
// The handlers run with a nil service: a missing description is rejected (400)
// before the service is reached; a valid one reaches it (nil svc then panics,
// recovered as 500), which is exactly what proves the guard let it through.
func descriptionApp(h *handlers) *fiber.App {
	app := fiber.New()
	app.Use(recover.New())
	app.Post("/sandbox/credit", h.sandboxCredit)
	app.Post("/sandbox/debit", h.sandboxDebit)
	app.Post("/real/debit", h.realDebit)
	app.Post("/game/cashout", h.cashoutGame)
	app.Post("/sandbox-purchase", h.m2mPurchaseSandbox)
	app.Post("/product-purchase", h.m2mPurchaseProduct)
	app.Post("/charge", h.m2mOpenCharge)
	return app
}

var descriptionBodies = map[string]string{
	"/sandbox/credit":   `{"user_id":"u1","amount":100,"idempotency_key":"k1"%s}`,
	"/sandbox/debit":    `{"user_id":"u1","amount":100,"idempotency_key":"k1"%s}`,
	"/real/debit":       `{"user_id":"u1","amount":100,"idempotency_key":"k1"%s}`,
	"/game/cashout":     `{"user_id":"u1","amount":100,"table_ref":"t1","hold_ids":["h1"],"idempotency_key":"k1"%s}`,
	"/sandbox-purchase": `{"user_id":"u1","sku":"pack_100","idempotency_key":"k1"%s}`,
	"/product-purchase": `{"user_id":"u1","sku":"poker_reaction_fire","idempotency_key":"k1"%s}`,
	"/charge":           `{"user_id":"u1","amount_cents":1000,"reference":"inv1","idempotency_key":"k1"%s}`,
}

func postRaw(t *testing.T, app *fiber.App, path, body string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode
}

func TestM2MRoutesRequireDescriptionWhenFlagOn(t *testing.T) {
	app := descriptionApp(&handlers{requireDescription: true})
	for path, tmpl := range descriptionBodies {
		without := sprintfOnce(tmpl, "")
		if code := postRaw(t, app, path, without); code != http.StatusBadRequest {
			t.Errorf("%s without description: status %d, want 400", path, code)
		}
		blank := sprintfOnce(tmpl, `,"description":"   "`)
		if code := postRaw(t, app, path, blank); code != http.StatusBadRequest {
			t.Errorf("%s with blank description: status %d, want 400", path, code)
		}
		with := sprintfOnce(tmpl, `,"description":"Mesa #t1, teste"`)
		if code := postRaw(t, app, path, with); code == http.StatusBadRequest {
			t.Errorf("%s: a valid description was rejected", path)
		}
	}
}

func TestM2MRoutesKeepDescriptionOptionalWhenFlagOff(t *testing.T) {
	app := descriptionApp(&handlers{requireDescription: false})
	for path, tmpl := range descriptionBodies {
		if code := postRaw(t, app, path, sprintfOnce(tmpl, "")); code == http.StatusBadRequest {
			t.Errorf("%s: flag off must keep description optional (got 400)", path)
		}
	}
}

func TestOversizeDescriptionIsRejectedNotTruncated(t *testing.T) {
	app := descriptionApp(&handlers{requireDescription: false})
	long := make([]byte, 256)
	for i := range long {
		long[i] = 'a'
	}
	body := sprintfOnce(descriptionBodies["/sandbox/credit"], `,"description":"`+string(long)+`"`)
	if code := postRaw(t, app, "/sandbox/credit", body); code != http.StatusUnprocessableEntity {
		t.Fatalf("256-char description: status %d, want 422 (validator rejects, never truncates)", code)
	}
}

func sprintfOnce(tmpl, extra string) string { return fmt.Sprintf(tmpl, extra) }
