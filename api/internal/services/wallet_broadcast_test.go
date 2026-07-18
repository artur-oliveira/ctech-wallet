package services

import (
	"context"
	"encoding/json"
	"testing"

	"gopkg.aoctech.app/api/internal/domain/wallet"
	"gopkg.aoctech.app/api/internal/kycclient"
	"gopkg.aoctech.app/api/internal/pix"
)

type fakeBroadcaster struct {
	userID  string
	payload []byte
	calls   int
}

func (f *fakeBroadcaster) Broadcast(_ context.Context, userID string, payload []byte) {
	f.userID = userID
	f.payload = payload
	f.calls++
}

// TestConfirmDepositBroadcastsOnCredit mirrors
// TestConfirmDepositCreditsOnCPFMatch's setup (same file) and adds a
// fakeBroadcaster to confirm a successfully credited deposit triggers exactly
// one Broadcast call carrying a deposit_confirmed payload.
func TestConfirmDepositBroadcastsOnCredit(t *testing.T) {
	repo := newStubRepo()
	repo.deposit = &wallet.PixDeposit{Txid: "tx-broadcast", WalletID: "w-real", UserID: "u1", AmountExpected: 500, Status: wallet.DepositPending}
	fake := pix.NewFake()
	fake.StageCharge("tx-broadcast", 500, pix.ChargeCompleted, "12345678901", "E2E-1")
	kyc := &stubKYC{rec: &kycclient.KYC{Level: "verified", CPF: "12345678901"}}

	svc := newSvc(repo, &stubLocker{}, fake, kyc)
	fb := &fakeBroadcaster{}
	svc.SetBroadcaster(fb)

	if err := svc.ConfirmDeposit(context.Background(), "tx-broadcast", "12345678901", "Someone"); err != nil {
		t.Fatalf("ConfirmDeposit: %v", err)
	}
	if fb.calls != 1 {
		t.Fatalf("expected 1 broadcast, got %d", fb.calls)
	}
	if fb.userID != "u1" {
		t.Fatalf("broadcast userID = %q, want u1", fb.userID)
	}
	var msg map[string]any
	if err := json.Unmarshal(fb.payload, &msg); err != nil {
		t.Fatalf("unmarshal broadcast payload: %v", err)
	}
	if msg["type"] != "deposit_confirmed" {
		t.Fatalf("bad payload: %+v", msg)
	}
}

// TestWithdrawBroadcastsOnComplete proves the synchronous Withdraw path
// notifies the user over WebSocket the same way ConfirmDeposit does.
func TestWithdrawBroadcastsOnComplete(t *testing.T) {
	repo := newStubRepo()
	fake := pix.NewFake()
	kyc := &stubKYC{rec: &kycclient.KYC{Level: "verified", CPF: "12345678901"}}
	svc := newSvc(repo, &stubLocker{}, fake, kyc)
	fb := &fakeBroadcaster{}
	svc.SetBroadcaster(fb)

	if _, err := svc.Withdraw(context.Background(), "u1", "verified", 5000, "idem-broadcast"); err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	if fb.calls != 1 || fb.userID != "u1" {
		t.Fatalf("expected 1 broadcast to u1, got calls=%d userID=%q", fb.calls, fb.userID)
	}
	var msg map[string]any
	if err := json.Unmarshal(fb.payload, &msg); err != nil {
		t.Fatalf("unmarshal broadcast payload: %v", err)
	}
	if msg["type"] != "withdraw_completed" {
		t.Fatalf("bad payload: %+v", msg)
	}
}

// TestWithdrawBroadcastsOnKeyNotFound proves the immediate-refund path (an
// unregistered PIX key) also notifies the user over WebSocket — it reuses
// reverse(), the same helper the async reconciliation path uses.
func TestWithdrawBroadcastsOnKeyNotFound(t *testing.T) {
	repo := newStubRepo()
	fake := pix.NewFake()
	fake.TransferErr = pix.ErrKeyNotFound
	kyc := &stubKYC{rec: &kycclient.KYC{Level: "verified", CPF: "12345678901"}}
	svc := newSvc(repo, &stubLocker{}, fake, kyc)
	fb := &fakeBroadcaster{}
	svc.SetBroadcaster(fb)

	if _, err := svc.Withdraw(context.Background(), "u1", "verified", 5000, "idem-broadcast2"); err == nil {
		t.Fatal("expected pix-key-not-found error")
	}
	if fb.calls != 1 || fb.userID != "u1" {
		t.Fatalf("expected 1 broadcast to u1, got calls=%d userID=%q", fb.calls, fb.userID)
	}
	var msg map[string]any
	if err := json.Unmarshal(fb.payload, &msg); err != nil {
		t.Fatalf("unmarshal broadcast payload: %v", err)
	}
	if msg["type"] != "withdraw_reversed" {
		t.Fatalf("bad payload: %+v", msg)
	}
}

// TestConfirmDepositNilBroadcasterIsNoOp confirms a service with no
// SetBroadcaster call (the state of every other existing ConfirmDeposit test
// in this file, and of cmd/reconcile's service) still credits successfully —
// broadcasting must never be load-bearing for the deposit itself.
func TestConfirmDepositNilBroadcasterIsNoOp(t *testing.T) {
	repo := newStubRepo()
	repo.deposit = &wallet.PixDeposit{Txid: "tx-nobroadcast", WalletID: "w-real", UserID: "u1", AmountExpected: 500, Status: wallet.DepositPending}
	fake := pix.NewFake()
	fake.StageCharge("tx-nobroadcast", 500, pix.ChargeCompleted, "12345678901", "E2E-1")
	kyc := &stubKYC{rec: &kycclient.KYC{Level: "verified", CPF: "12345678901"}}

	svc := newSvc(repo, &stubLocker{}, fake, kyc)
	if err := svc.ConfirmDeposit(context.Background(), "tx-nobroadcast", "12345678901", "Someone"); err != nil {
		t.Fatalf("ConfirmDeposit: %v", err)
	}
	if repo.depositStatus != wallet.DepositConfirmed {
		t.Fatalf("deposit status = %q, want confirmed", repo.depositStatus)
	}
}
