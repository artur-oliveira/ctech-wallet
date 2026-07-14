package inter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

// newTestClient builds an InterClient against a plain (non-mTLS) httptest
// server, so tests never touch SSM or real Inter. Credentials are set directly
// for the GetToken op.
func newTestClient(base string, httpClient *http.Client) *InterClient {
	base = strings.TrimRight(base, "/")
	return &InterClient{
		base:         base,
		pixKey:       "test-pix-key",
		http:         httpClient,
		clientID:     "cid",
		clientSecret: "csec",
		scope:        tokenScope,
		tokenURL:     base + pathToken,
	}
}

func TestGetToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != pathToken {
			t.Errorf("unexpected token path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "AT", "expires_in": 3600})
	}))
	defer srv.Close()

	c := newTestClient(srv.URL, srv.Client())
	res, err := c.GetToken(context.Background())
	if err != nil {
		t.Fatalf("GetToken: %v", err)
	}
	if res.Token != "AT" || res.ExpiresIn != 3600 {
		t.Fatalf("bad token result: %+v", res)
	}
}

// TestDoSetsBearer verifies the bearer api passes per call is forwarded as
// Authorization: Bearer <tok> — pix-gateway is a pure transport.
func TestDoSetsBearer(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"txid": "tx1", "status": ChargeActive, "pixCopiaECola": "EMV"})
	}))
	defer srv.Close()

	c := newTestClient(srv.URL, srv.Client())
	ctx := WithBearer(context.Background(), "BEARER123")
	ch, err := c.CreateCharge(ctx, "tx1", 500, "")
	if err != nil {
		t.Fatalf("CreateCharge: %v", err)
	}
	if gotAuth != "Bearer BEARER123" {
		t.Fatalf("expected Authorization Bearer BEARER123, got %q", gotAuth)
	}
	if ch.Txid != "tx1" || ch.Status != ChargeActive {
		t.Fatalf("bad charge: %+v", ch)
	}
}

// TestDoMissingBearer proves the tokenManager is gone: without a supplied
// bearer, do refuses instead of fetching one.
func TestDoMissingBearer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	c := newTestClient(srv.URL, srv.Client())
	_, err := c.CreateCharge(context.Background(), "tx1", 500, "")
	if err == nil || !strings.Contains(err.Error(), "missing OAuth bearer") {
		t.Fatalf("expected missing-bearer error, got %v", err)
	}
}

func TestPingValidatesBearer(t *testing.T) {
	c := newTestClient("http://example.invalid", &http.Client{})
	if err := c.Ping(WithBearer(context.Background(), "X")); err != nil {
		t.Fatalf("Ping with bearer should succeed: %v", err)
	}
	if err := c.Ping(context.Background()); err == nil {
		t.Fatal("Ping without bearer should fail")
	}
}
