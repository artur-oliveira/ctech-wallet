package services

import (
	"context"
	"errors"
	"testing"

	"github.com/artur-oliveira/ctech-wallet/api/internal/domain/wallet"
	"github.com/artur-oliveira/ctech-wallet/api/internal/kycclient"
	"github.com/artur-oliveira/ctech-wallet/api/internal/pix"
	"github.com/artur-oliveira/ctech-wallet/api/internal/repositories"
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
func (r *reconRepo) Credit(_ context.Context, _ repositories.Mutation) (*wallet.LedgerEntry, bool, error) {
	if r.creditErr != nil {
		return nil, false, r.creditErr
	}
	r.credited++
	return &wallet.LedgerEntry{}, false, nil
}

func newReconSvc(repo Repo, pc pix.PixClient) *WalletService {
	return NewWalletService(repo, &stubUserRepo{}, &stubAudit{}, &stubLocker{}, pc, &stubKYC{rec: &kycclient.KYC{}})
}

func TestReconcileCompletesDonePayout(t *testing.T) {
	repo := &reconRepo{stubRepo: newStubRepo(), processing: []wallet.Withdrawal{
		{WithdrawalID: "wd1", WalletID: "w-real", Amount: 5000, Fee: 100, Status: wallet.WithdrawProcessing},
	}}
	fake := pix.NewFake()
	fake.StageTransferStatus("wd1", pix.TransferDone)
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
		{WithdrawalID: "wd2", WalletID: "w-real", Amount: 5000, Fee: 100, Status: wallet.WithdrawProcessing},
	}}
	repo.withdrawals["wd2"] = &repo.processing[0]
	fake := pix.NewFake() // no staged status → NAO_ENCONTRADO

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
		{WithdrawalID: "wd3", WalletID: "w-real", Amount: 5000, Fee: 100, Status: wallet.WithdrawProcessing},
	}}
	repo.withdrawals["wd3"] = &repo.processing[0]
	fake := pix.NewFake()

	_, reversed, alarmed, err := newReconSvc(repo, fake).ReconcileWithdrawals(context.Background())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if reversed != 0 || alarmed != 1 {
		t.Fatalf("got reversed=%d alarmed=%d", reversed, alarmed)
	}
	if repo.withdrawals["wd3"].Status != wallet.WithdrawRefundFail {
		t.Errorf("status = %q, want refund_failed", repo.withdrawals["wd3"].Status)
	}
}
