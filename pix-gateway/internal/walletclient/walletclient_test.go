package walletclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"gopkg.aoctech.app/wallet/pix-gateway/internal/config"
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
	if err := c.ConfirmDeposit(context.Background(), "tx1", "***137303**", "Artur Oliveira Carvalho"); err != nil {
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
	if err := c.ConfirmDeposit(context.Background(), "tx1", "", ""); err == nil {
		t.Fatal("expected an error on 500")
	}
}

func TestConfirmSandboxPurchaseSendsBearerAndTxid(t *testing.T) {
	accountSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok-abc", "expires_in": 3600})
	}))
	defer accountSrv.Close()

	var gotAuth, gotTxid, gotPath string
	walletSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get(headerAuthorization)
		gotPath = r.URL.Path
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotTxid = body["txid"]
		w.WriteHeader(http.StatusOK)
	}))
	defer walletSrv.Close()

	c := New(&config.Config{
		CtechURL: accountSrv.URL, PixGatewayClientID: "pix-gateway", WalletAPIURL: walletSrv.URL,
	}, "secret")
	if err := c.ConfirmSandboxPurchase(context.Background(), "sbxp123"); err != nil {
		t.Fatalf("ConfirmSandboxPurchase: %v", err)
	}
	if gotAuth != bearerPrefix+"tok-abc" || gotPath != pathConfirmSandboxPurchase || gotTxid != "sbxp123" {
		t.Fatalf("request auth=%q path=%q txid=%q", gotAuth, gotPath, gotTxid)
	}
}

func TestConfirmProductPurchaseSendsBearerAndTxid(t *testing.T) {
	accountSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok-abc", "expires_in": 3600})
	}))
	defer accountSrv.Close()

	var gotAuth, gotTxid, gotPath string
	walletSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get(headerAuthorization)
		gotPath = r.URL.Path
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotTxid = body["txid"]
		w.WriteHeader(http.StatusOK)
	}))
	defer walletSrv.Close()

	c := New(&config.Config{
		CtechURL: accountSrv.URL, PixGatewayClientID: "pix-gateway", WalletAPIURL: walletSrv.URL,
	}, "secret")
	if err := c.ConfirmProductPurchase(context.Background(), "prdp123"); err != nil {
		t.Fatalf("ConfirmProductPurchase: %v", err)
	}
	if gotAuth != bearerPrefix+"tok-abc" || gotPath != pathConfirmProductPurchase || gotTxid != "prdp123" {
		t.Fatalf("request auth=%q path=%q txid=%q", gotAuth, gotPath, gotTxid)
	}
}
