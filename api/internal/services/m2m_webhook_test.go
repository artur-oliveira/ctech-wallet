package services

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
)

// TestDispatchM2MWebhookDeliversSignedPayload guards the notify-back
// contract: the registered URL receives the purchase's terminal state,
// correctly HMAC-signed with the registered client's own secret.
func TestDispatchM2MWebhookDeliversSignedPayload(t *testing.T) {
	const secret = "poker-secret"
	var gotBody []byte
	var gotSig string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotSig = r.Header.Get(HeaderM2MWebhookSignature)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	repo := newStubRepo()
	purchases := newStubSandboxPurchaseRepo()
	svc := newSandboxSvc(repo, purchases, nil)
	svc.SetM2MClients(map[string]M2MClient{"poker": {WebhookURL: srv.URL, HMACSecret: secret}})

	p := &wallet.SandboxPurchase{
		PurchaseID: "sbxp#poker#u1#idem-1", UserID: "u1", SKU: "pack_100",
		Status: wallet.SandboxPurchaseConfirmed, AmountExpected: 100, CreditsGranted: 1000,
		RequestingClient: "poker",
	}
	purchases.purchases[p.PurchaseID] = p

	svc.dispatchM2MWebhook(context.Background(), p)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(gotBody)
	wantSig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if gotSig != wantSig {
		t.Fatalf("signature mismatch: got %q want %q", gotSig, wantSig)
	}
	var payload m2mWebhookPayload
	if err := json.Unmarshal(gotBody, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.PurchaseID != p.PurchaseID || payload.Status != wallet.SandboxPurchaseConfirmed {
		t.Fatalf("unexpected payload: %+v", payload)
	}
	updated, _ := purchases.Get(context.Background(), p.PurchaseID)
	if updated.WebhookStatus != wallet.WebhookDelivered {
		t.Fatalf("expected webhook_status delivered, got %q", updated.WebhookStatus)
	}
}

// TestDispatchM2MWebhookMarksFailedOnNon2xx guards the retry sweep's work
// queue: a non-2xx response must be recorded as failed, not silently dropped.
func TestDispatchM2MWebhookMarksFailedOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	repo := newStubRepo()
	purchases := newStubSandboxPurchaseRepo()
	svc := newSandboxSvc(repo, purchases, nil)
	svc.SetM2MClients(map[string]M2MClient{"poker": {WebhookURL: srv.URL, HMACSecret: "s"}})

	p := &wallet.SandboxPurchase{PurchaseID: "sbxp#poker#u1#idem-2", UserID: "u1", RequestingClient: "poker", Status: wallet.SandboxPurchaseConfirmed}
	purchases.purchases[p.PurchaseID] = p

	svc.dispatchM2MWebhook(context.Background(), p)

	updated, _ := purchases.Get(context.Background(), p.PurchaseID)
	if updated.WebhookStatus != wallet.WebhookFailed {
		t.Fatalf("expected webhook_status failed, got %q", updated.WebhookStatus)
	}
}

// TestDispatchM2MWebhookNoopForUserDirectPurchase: a purchase the user opened
// directly (RequestingClient empty) has nothing to notify — dispatch must not
// touch webhook_status at all.
func TestDispatchM2MWebhookNoopForUserDirectPurchase(t *testing.T) {
	repo := newStubRepo()
	purchases := newStubSandboxPurchaseRepo()
	svc := newSandboxSvc(repo, purchases, nil)

	p := &wallet.SandboxPurchase{PurchaseID: "sbxp#u1#idem-3", UserID: "u1", Status: wallet.SandboxPurchaseConfirmed}
	purchases.purchases[p.PurchaseID] = p

	svc.dispatchM2MWebhook(context.Background(), p)

	updated, _ := purchases.Get(context.Background(), p.PurchaseID)
	if updated.WebhookStatus != "" {
		t.Fatalf("expected webhook_status untouched, got %q", updated.WebhookStatus)
	}
}

// TestDispatchM2MWebhookMissingClientConfigMarksFailed: a scope-holding M2M
// client with no registered webhook URL is a deploy misconfiguration, not a
// silent no-op — it must show up in the retry sweep's work queue.
func TestDispatchM2MWebhookMissingClientConfigMarksFailed(t *testing.T) {
	repo := newStubRepo()
	purchases := newStubSandboxPurchaseRepo()
	svc := newSandboxSvc(repo, purchases, nil)
	svc.SetM2MClients(map[string]M2MClient{}) // "poker" never registered

	p := &wallet.SandboxPurchase{PurchaseID: "sbxp#poker#u1#idem-4", UserID: "u1", RequestingClient: "poker", Status: wallet.SandboxPurchaseConfirmed}
	purchases.purchases[p.PurchaseID] = p

	svc.dispatchM2MWebhook(context.Background(), p)

	updated, _ := purchases.Get(context.Background(), p.PurchaseID)
	if updated.WebhookStatus != wallet.WebhookFailed {
		t.Fatalf("expected webhook_status failed for unregistered client, got %q", updated.WebhookStatus)
	}
}

// TestRetryFailedM2MWebhooksRetriesOnlyFailed guards the reconcile sweep's
// selectivity: it must re-attempt exactly the failed rows, and a retry that
// now succeeds must flip the row to delivered.
func TestRetryFailedM2MWebhooksRetriesOnlyFailed(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	repo := newStubRepo()
	purchases := newStubSandboxPurchaseRepo()
	svc := newSandboxSvc(repo, purchases, nil)
	svc.SetM2MClients(map[string]M2MClient{"poker": {WebhookURL: srv.URL, HMACSecret: "s"}})

	failed := &wallet.SandboxPurchase{
		PurchaseID: "sbxp#poker#u1#idem-5", UserID: "u1", RequestingClient: "poker",
		Status: wallet.SandboxPurchaseConfirmed, WebhookStatus: wallet.WebhookFailed,
		CreatedAt: "2020-01-01T00:00:00Z", UpdatedAt: "2020-01-01T00:00:00Z",
	}
	delivered := &wallet.SandboxPurchase{
		PurchaseID: "sbxp#poker#u1#idem-6", UserID: "u1", RequestingClient: "poker",
		Status: wallet.SandboxPurchaseConfirmed, WebhookStatus: wallet.WebhookDelivered,
		CreatedAt: "2020-01-01T00:00:00Z", UpdatedAt: "2020-01-01T00:00:00Z",
	}
	purchases.purchases[failed.PurchaseID] = failed
	purchases.purchases[delivered.PurchaseID] = delivered

	retried, err := svc.RetryFailedM2MWebhooks(context.Background())
	if err != nil {
		t.Fatalf("RetryFailedM2MWebhooks: %v", err)
	}
	if retried != 1 || hits != 1 {
		t.Fatalf("expected exactly 1 retry, got retried=%d hits=%d", retried, hits)
	}
	updated, _ := purchases.Get(context.Background(), failed.PurchaseID)
	if updated.WebhookStatus != wallet.WebhookDelivered {
		t.Fatalf("expected the retried purchase to flip to delivered, got %q", updated.WebhookStatus)
	}
}
