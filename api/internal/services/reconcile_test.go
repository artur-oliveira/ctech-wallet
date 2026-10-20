package services

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/api-commons/alerts"
	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/kycclient"
	"gopkg.aoctech.app/wallet/api/internal/rail"
	"gopkg.aoctech.app/wallet/api/internal/repositories"
)

// reconRepo extends the behavior needed for reconciliation tests.
type reconRepo struct {
	*stubRepo
	processing []wallet.Withdrawal
	creditErr  error
	credited   int
}

func (r *reconRepo) ListProcessingWithdrawals(_ context.Context, _ int) ([]wallet.Withdrawal, error) {
	return r.processing, nil
}
func (r *reconRepo) Credit(_ context.Context, _ repositories.Mutation, _ ...types.TransactWriteItem) (*wallet.LedgerEntry, bool, error) {
	if r.creditErr != nil {
		return nil, false, r.creditErr
	}
	r.credited++
	return &wallet.LedgerEntry{}, false, nil
}

func newReconSvc(repo Repo, pc rail.Rail) *WalletService {
	return NewWalletService(repo, &stubUserRepo{}, &stubAudit{}, &stubLocker{}, pc, &stubKYC{rec: &kycclient.KYC{}})
}

func TestReconcileCompletesDonePayout(t *testing.T) {
	repo := &reconRepo{stubRepo: newStubRepo(), processing: []wallet.Withdrawal{
		{WithdrawalID: "wd1", WalletID: "w-real", Amount: 5000, Status: wallet.WithdrawProcessing},
	}}
	fake := rail.NewFake()
	fake.StageTransferStatus(interIdemKey("wd1"), rail.TransferDone)
	repo.withdrawals["wd1"] = &repo.processing[0]

	resolved, reversed, alarmed, err := newReconSvc(repo, fake).ReconcileWithdrawals(context.Background())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if resolved != 1 || reversed != 0 || alarmed != 0 {
		t.Fatalf("got resolved=%d reversed=%d alarmed=%d", resolved, reversed, alarmed)
	}
	if repo.withdrawals["wd1"].Status != wallet.WithdrawCompleted {
		t.Errorf("status = %q, want completed", repo.withdrawals["wd1"].Status)
	}
}

func TestReconcileReversesMissingPayout(t *testing.T) {
	repo := &reconRepo{stubRepo: newStubRepo(), processing: []wallet.Withdrawal{
		{WithdrawalID: "wd2", WalletID: "w-real", Amount: 5000, Status: wallet.WithdrawProcessing},
	}}
	repo.withdrawals["wd2"] = &repo.processing[0]
	fake := rail.NewFake() // no staged status → NAO_ENCONTRADO

	resolved, reversed, alarmed, err := newReconSvc(repo, fake).ReconcileWithdrawals(context.Background())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if resolved != 0 || reversed != 1 || alarmed != 0 {
		t.Fatalf("got resolved=%d reversed=%d alarmed=%d", resolved, reversed, alarmed)
	}
	if repo.credited != 1 {
		t.Errorf("expected one credit-back, got %d", repo.credited)
	}
	if repo.withdrawals["wd2"].Status != wallet.WithdrawReversed {
		t.Errorf("status = %q, want reversed", repo.withdrawals["wd2"].Status)
	}
}

func TestReconcileAlarmsOnFailedReversal(t *testing.T) {
	repo := &reconRepo{stubRepo: newStubRepo(), creditErr: errors.New("dynamo down"), processing: []wallet.Withdrawal{
		{WithdrawalID: "wd3", WalletID: "w-real", Amount: 5000, Status: wallet.WithdrawProcessing},
	}}
	repo.withdrawals["wd3"] = &repo.processing[0]
	fake := rail.NewFake()

	svc := newReconSvc(repo, fake)
	sent := &recordingAlerts{}
	svc.SetAlerts(sent)
	_, reversed, alarmed, err := svc.ReconcileWithdrawals(context.Background())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if reversed != 0 || alarmed != 1 {
		t.Fatalf("got reversed=%d alarmed=%d", reversed, alarmed)
	}
	if repo.withdrawals["wd3"].Status != wallet.WithdrawRefundFail {
		t.Errorf("status = %q, want refund_failed", repo.withdrawals["wd3"].Status)
	}
	if len(sent.got) != 1 || sent.got[0].Job != alertJobWithdrawReversal || !strings.Contains(sent.got[0].Detail, "wd3") {
		t.Errorf("alerts = %+v, want one %s alert naming wd3", sent.got, alertJobWithdrawReversal)
	}
}

// TestReconcileBroadcastsOnComplete and the two tests below prove the async
// reconciliation path notifies the user over WebSocket exactly like the
// synchronous Withdraw path does — the user must never be left wondering
// what happened just because the outcome was resolved out-of-band.
func TestReconcileBroadcastsOnComplete(t *testing.T) {
	repo := &reconRepo{stubRepo: newStubRepo(), processing: []wallet.Withdrawal{
		{WithdrawalID: "wd1", WalletID: "w-real", UserID: "u1", Amount: 5000, Status: wallet.WithdrawProcessing},
	}}
	fake := rail.NewFake()
	fake.StageTransferStatus(interIdemKey("wd1"), rail.TransferDone)
	repo.withdrawals["wd1"] = &repo.processing[0]

	svc := newReconSvc(repo, fake)
	fb := &fakeBroadcaster{}
	svc.SetBroadcaster(fb)

	if _, _, _, err := svc.ReconcileWithdrawals(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
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

func TestReconcileBroadcastsOnReversal(t *testing.T) {
	repo := &reconRepo{stubRepo: newStubRepo(), processing: []wallet.Withdrawal{
		{WithdrawalID: "wd2", WalletID: "w-real", UserID: "u1", Amount: 5000, Status: wallet.WithdrawProcessing},
	}}
	repo.withdrawals["wd2"] = &repo.processing[0]
	fake := rail.NewFake() // no staged status → NAO_ENCONTRADO

	svc := newReconSvc(repo, fake)
	fb := &fakeBroadcaster{}
	svc.SetBroadcaster(fb)

	if _, _, _, err := svc.ReconcileWithdrawals(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	var msg map[string]any
	if err := json.Unmarshal(fb.payload, &msg); err != nil {
		t.Fatalf("unmarshal broadcast payload: %v", err)
	}
	if msg["type"] != "withdraw_reversed" {
		t.Fatalf("bad payload: %+v", msg)
	}
}

func TestReconcileBroadcastsOnAlarmedReversal(t *testing.T) {
	repo := &reconRepo{stubRepo: newStubRepo(), creditErr: errors.New("dynamo down"), processing: []wallet.Withdrawal{
		{WithdrawalID: "wd3", WalletID: "w-real", UserID: "u1", Amount: 5000, Status: wallet.WithdrawProcessing},
	}}
	repo.withdrawals["wd3"] = &repo.processing[0]
	fake := rail.NewFake()

	svc := newReconSvc(repo, fake)
	fb := &fakeBroadcaster{}
	svc.SetBroadcaster(fb)

	if _, _, _, err := svc.ReconcileWithdrawals(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	var msg map[string]any
	if err := json.Unmarshal(fb.payload, &msg); err != nil {
		t.Fatalf("unmarshal broadcast payload: %v", err)
	}
	if msg["type"] != "withdraw_refund_failed" {
		t.Fatalf("bad payload: %+v", msg)
	}
}

func TestReconcileReversalReturnsTheDailyWithdrawSlot(t *testing.T) {
	created := time.Now().Format(time.RFC3339Nano)
	repo := &reconRepo{stubRepo: newStubRepo(), processing: []wallet.Withdrawal{
		{WithdrawalID: "wd4", WalletID: "w-real", UserID: "u1", Amount: 5000, Status: wallet.WithdrawProcessing, CreatedAt: created},
	}}
	repo.withdrawals["wd4"] = &repo.processing[0]
	day, _, _ := wallet.WindowKeys(time.Now())
	users := &stubUserRepo{user: &wallet.User{RealDailyCounters: &wallet.RealDailyCounters{DayKey: day, WithdrawCount: 1, WithdrawSum: 5000}}}
	svc := NewWalletService(repo, users, &stubAudit{}, &stubLocker{}, rail.NewFake(), &stubKYC{rec: &kycclient.KYC{}})

	if _, reversed, _, err := svc.ReconcileWithdrawals(context.Background()); err != nil || reversed != 1 {
		t.Fatalf("reversed=%d err=%v", reversed, err)
	}
	got := users.user.RealDailyCounters
	if got.WithdrawCount != 0 || got.WithdrawSum != 0 {
		t.Fatalf("slot not returned: %+v", got)
	}
}

type recordingAlerts struct{ got []alerts.Alert }

func (r *recordingAlerts) Alert(_ context.Context, a alerts.Alert) { r.got = append(r.got, a) }
