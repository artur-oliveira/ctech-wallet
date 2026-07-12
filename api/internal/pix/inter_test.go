package pix

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCentavosReaisRoundTrip(t *testing.T) {
	cases := []int64{1, 99, 100, 12345, 5000, 100000}
	for _, c := range cases {
		if got := reaisToCentavos(centavosToReais(c)); got != c {
			t.Errorf("round-trip %d: got %d (via %q)", c, got, centavosToReais(c))
		}
	}
	if centavosToReais(12345) != "123.45" {
		t.Errorf("format: got %q", centavosToReais(12345))
	}
}

func TestInterCreateChargeAndTokenReuse(t *testing.T) {
	var tokenCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == pathToken:
			atomic.AddInt32(&tokenCalls, 1)
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok-123", "expires_in": 3600})
		case strings.HasPrefix(r.URL.Path, "/pix/v2/cob/"):
			if got := r.Header.Get("Authorization"); got != "Bearer tok-123" {
				t.Errorf("missing/bad bearer: %q", got)
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["chave"] != "wallet-key" {
				t.Errorf("chave not sent: %v", body)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"txid": "tx1", "status": ChargeActive, "pixCopiaECola": "EMV-PAYLOAD",
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := &InterClient{
		base:   srv.URL,
		pixKey: "wallet-key",
		http:   srv.Client(),
	}
	c.tokens = &tokenManager{client: srv.Client(), tokenURL: srv.URL + pathToken, clientID: "id", clientSecret: "sec", scope: tokenScope}

	ctx := context.Background()
	ch, err := c.CreateCharge(ctx, "tx1", 12345, "")
	if err != nil {
		t.Fatalf("CreateCharge: %v", err)
	}
	if ch.QRCode != "EMV-PAYLOAD" || ch.Status != ChargeActive || ch.Amount != 12345 {
		t.Fatalf("bad charge: %+v", ch)
	}

	// Second call reuses the cached token.
	if _, err := c.CreateCharge(ctx, "tx1", 12345, ""); err != nil {
		t.Fatalf("second CreateCharge: %v", err)
	}
	if got := atomic.LoadInt32(&tokenCalls); got != 1 {
		t.Errorf("token fetched %d times, want 1 (should be cached)", got)
	}
}

func TestFakeSatisfiesInterface(t *testing.T) {
	f := NewFake()
	f.StageCharge("tx", 500, ChargeCompleted, "12345678901", "E2E-1")
	ch, err := f.QueryCharge(context.Background(), "tx")
	if err != nil || ch.PayerCPF != "12345678901" {
		t.Fatalf("fake query: %+v err=%v", ch, err)
	}
}
