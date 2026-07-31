package v1

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestAsaasCentavosExactParsing(t *testing.T) {
	tests := []struct {
		json string
		want int64
		ok   bool
	}{
		{`1`, 100, true},
		{`1.2`, 120, true},
		{`1.23`, 123, true},
		{`0.01`, 1, true},
		{`0`, 0, false},
		{`-1`, 0, false},
		{`1.001`, 0, false},
		{`1e3`, 0, false},
		{`92233720368547758.08`, 0, false},
	}
	for _, tt := range tests {
		var got asaasCentavos
		err := json.Unmarshal([]byte(tt.json), &got)
		if (err == nil) != tt.ok {
			t.Errorf("%s: err=%v, want ok=%v", tt.json, err, tt.ok)
		}
		if err == nil && int64(got) != tt.want {
			t.Errorf("%s: got %d want %d", tt.json, got, tt.want)
		}
	}
}

func TestUnverifiableAsaasEventsAreQuarantined(t *testing.T) {
	app := fiber.New()
	h := &handlers{} // nil services prove these events cannot mutate state
	app.Post("/webhook", h.asaasWebhook)

	for _, body := range []string{
		`{"event":"ACCOUNT_STATUS_APPROVED","account":{"id":"acc_1","status":"ACCOUNT_STATUS_APPROVED"}}`,
		`{"event":"TRANSFER_MED_CLAWBACK","account":{"id":"acc_1"},"transfer":{"value":10.00,"externalReference":"med_1"}}`,
	} {
		req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(body))
		req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("webhook: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Errorf("status=%d want 200", resp.StatusCode)
		}
	}
}
