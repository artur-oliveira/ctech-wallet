package walletclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/artur-oliveira/ctech-wallet/pix-gateway/internal/config"
)

func TestConfirmDepositSendsBearerAndTxid(t *testing.T) {
	var gotAuth, gotBody string
	accountSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == pathToken {
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok-abc", "expires_in": 3600})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer accountSrv.Close()

	walletSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		body, _ := json.Marshal(map[string]any{})
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotBody = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer walletSrv.Close()

	cfg := &config.Config{
		CtechURL:               accountSrv.URL,
		PixGatewayClientID:     "pix-gateway",
		PixGatewayClientSecret: "secret",
		WalletAPIURL:           walletSrv.URL,
	}
	c := New(cfg, cfg.PixGatewayClientSecret)
	if err := c.ConfirmDeposit(context.Background(), "tx1"); err != nil {
		t.Fatalf("ConfirmDeposit: %v", err)
	}
	if gotAuth != "Bearer tok-abc" {
		t.Fatalf("bad bearer: %q", gotAuth)
	}
	if gotBody != pathConfirmDeposit {
		t.Fatalf("bad path: %q", gotBody)
	}
}

func TestConfirmDepositErrorStatus(t *testing.T) {
	accountSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok-abc", "expires_in": 3600})
	}))
	defer accountSrv.Close()

	walletSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer walletSrv.Close()

	cfg := &config.Config{
		CtechURL:           accountSrv.URL,
		PixGatewayClientID: "pix-gateway",
		WalletAPIURL:       walletSrv.URL,
	}
	c := New(cfg, "secret")
	if err := c.ConfirmDeposit(context.Background(), "tx1"); err == nil {
		t.Fatal("expected an error on 500")
	}
}
