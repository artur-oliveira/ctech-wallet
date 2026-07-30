package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/kycclient"
	"gopkg.aoctech.app/wallet/api/internal/pix"
	"gopkg.aoctech.app/wallet/api/internal/problem"
	"gopkg.aoctech.app/wallet/api/internal/repositories"
)

type stubSandboxPurchaseRepo struct {
	purchases map[string]*wallet.SandboxPurchase
	putErr    error
}

func newStubSandboxPurchaseRepo() *stubSandboxPurchaseRepo {
	return &stubSandboxPurchaseRepo{purchases: map[string]*wallet.SandboxPurchase{}}
}

func (r *stubSandboxPurchaseRepo) PutIfAbsent(_ context.Context, p *wallet.SandboxPurchase) error {
	if r.putErr != nil {
		return r.putErr
	}
	if _, ok := r.purchases[p.PurchaseID]; ok {
		return repositories.ErrSandboxPurchaseExists
	}
	cp := *p
	r.purchases[p.PurchaseID] = &cp
	return nil
}

func (r *stubSandboxPurchaseRepo) Get(_ context.Context, purchaseID string) (*wallet.SandboxPurchase, error) {
	return r.purchases[purchaseID], nil
}

func (r *stubSandboxPurchaseRepo) Update(_ context.Context, purchaseID string, updates map[string]any) error {
	p := r.purchases[purchaseID]
	if p == nil {
		return nil
	}
	if v, ok := updates["status"].(string); ok {
		p.Status = v
	}
	if v, ok := updates["e2e_id"].(string); ok {
		p.E2EID = v
	}
	if v, ok := updates["credit_sk"].(string); ok {
		p.CreditSK = v
	}
	return nil
}

func (r *stubSandboxPurchaseRepo) ListPendingOlderThan(_ context.Context, _ time.Time, _ int) ([]wallet.SandboxPurchase, error) {
	var out []wallet.SandboxPurchase
	for _, p := range r.purchases {
		if p.Status == wallet.SandboxPurchasePending {
			out = append(out, *p)
		}
	}
	return out, nil
}

func newSandboxSvc(repo *stubRepo, purchases *stubSandboxPurchaseRepo, pc pix.PixClient) *WalletService {
	svc := newSvc(repo, &stubLocker{}, pc, &stubKYC{rec: &kycclient.KYC{}})
	svc.SetSandboxPurchases(purchases)
	return svc
}

func TestPurchaseSandboxDirectOpensChargeForValidSKU(t *testing.T) {
	repo := newStubRepo()
	purchases := newStubSandboxPurchaseRepo()
	fakePix := pix.NewFake()
	svc := newSandboxSvc(repo, purchases, fakePix)

	p, charge, err := svc.PurchaseSandboxDirect(context.Background(), "u1", "pack_100", "idem-1")
	if err != nil {
		t.Fatalf("PurchaseSandboxDirect: %v", err)
	}
	if p.AmountExpected != 100 || p.CreditsGranted != 100*wallet.SandboxCreditsPerCentavo {
		t.Fatalf("unexpected purchase: %+v", p)
	}
	if charge.Amount != 100 {
		t.Fatalf("unexpected charge amount: %d", charge.Amount)
	}
	if len(fakePix.CreatedCharges) != 1 {
		t.Fatalf("expected 1 CreateCharge call, got %d", len(fakePix.CreatedCharges))
	}
}

func TestPurchaseSandboxDirectRejectsUnknownSKU(t *testing.T) {
	repo := newStubRepo()
	purchases := newStubSandboxPurchaseRepo()
	svc := newSandboxSvc(repo, purchases, pix.NewFake())

	_, _, err := svc.PurchaseSandboxDirect(context.Background(), "u1", "not-a-sku", "idem-2")
	p, ok := errors.AsType[*problem.Problem](err)
	if !ok || p.Type != problem.TypeBadRequest {
		t.Fatalf("expected bad-request, got %v", err)
	}
}

func TestPurchaseSandboxDirectIdempotentReplay(t *testing.T) {
	repo := newStubRepo()
	purchases := newStubSandboxPurchaseRepo()
	fakePix := pix.NewFake()
	svc := newSandboxSvc(repo, purchases, fakePix)

	first, _, err := svc.PurchaseSandboxDirect(context.Background(), "u1", "pack_100", "idem-3")
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	second, _, err := svc.PurchaseSandboxDirect(context.Background(), "u1", "pack_100", "idem-3")
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if second.PurchaseID != first.PurchaseID {
		t.Fatalf("expected the same purchase on replay, got %q vs %q", second.PurchaseID, first.PurchaseID)
	}
	if len(fakePix.CreatedCharges) != 1 {
		t.Fatalf("expected replay to never open a second charge, got %d", len(fakePix.CreatedCharges))
	}
}

func TestConfirmSandboxPurchaseCreditsSandboxWallet(t *testing.T) {
	repo := newStubRepo()
	purchases := newStubSandboxPurchaseRepo()
	fakePix := pix.NewFake()
	svc := newSandboxSvc(repo, purchases, fakePix)

	p, _, err := svc.PurchaseSandboxDirect(context.Background(), "u1", "pack_100", "idem-4")
	if err != nil {
		t.Fatalf("PurchaseSandboxDirect: %v", err)
	}
	fakePix.StageCharge(p.PurchaseID, 100, pix.ChargeCompleted, "", "e2e-1")

	if err := svc.ConfirmSandboxPurchase(context.Background(), p.PurchaseID, false); err != nil {
		t.Fatalf("ConfirmSandboxPurchase: %v", err)
	}
	if len(repo.creditCalls) != 1 {
		t.Fatalf("expected 1 credit call, got %d", len(repo.creditCalls))
	}
	if repo.creditCalls[0].Amount != 100*wallet.SandboxCreditsPerCentavo {
		t.Fatalf("unexpected credited amount: %d", repo.creditCalls[0].Amount)
	}
	updated, err := purchases.Get(context.Background(), p.PurchaseID)
	if err != nil || updated.Status != wallet.SandboxPurchaseConfirmed {
		t.Fatalf("expected purchase confirmed, got %+v, err=%v", updated, err)
	}

	// Replay (webhook redelivery / sweep) must never double-credit.
	if err := svc.ConfirmSandboxPurchase(context.Background(), p.PurchaseID, true); err != nil {
		t.Fatalf("ConfirmSandboxPurchase replay: %v", err)
	}
	if len(repo.creditCalls) != 1 {
		t.Fatalf("expected replay to skip crediting again, got %d total calls", len(repo.creditCalls))
	}
}

func TestConfirmSandboxPurchaseNotYetPaidIsNoOp(t *testing.T) {
	repo := newStubRepo()
	purchases := newStubSandboxPurchaseRepo()
	fakePix := pix.NewFake()
	svc := newSandboxSvc(repo, purchases, fakePix)

	p, _, err := svc.PurchaseSandboxDirect(context.Background(), "u1", "pack_100", "idem-5")
	if err != nil {
		t.Fatalf("PurchaseSandboxDirect: %v", err)
	}
	// Charge exists (from CreateCharge) but is still ATIVA — never staged as paid.

	if err := svc.ConfirmSandboxPurchase(context.Background(), p.PurchaseID, false); err != nil {
		t.Fatalf("ConfirmSandboxPurchase: %v", err)
	}
	if len(repo.creditCalls) != 0 {
		t.Fatalf("expected no credit for an unpaid charge, got %d", len(repo.creditCalls))
	}
}

func TestRefundSandboxPurchaseEligibleWhenUnused(t *testing.T) {
	repo := newStubRepo()
	purchases := newStubSandboxPurchaseRepo()
	fakePix := pix.NewFake()
	svc := newSandboxSvc(repo, purchases, fakePix)

	p, _, err := svc.PurchaseSandboxDirect(context.Background(), "u1", "pack_100", "idem-6")
	if err != nil {
		t.Fatalf("PurchaseSandboxDirect: %v", err)
	}
	fakePix.StageCharge(p.PurchaseID, 100, pix.ChargeCompleted, "", "e2e-2")
	if err := svc.ConfirmSandboxPurchase(context.Background(), p.PurchaseID, false); err != nil {
		t.Fatalf("ConfirmSandboxPurchase: %v", err)
	}
	repo.anyDebitSince = false

	refunded, err := svc.RefundSandboxPurchase(context.Background(), "u1", p.PurchaseID, "idem-refund-1")
	if err != nil {
		t.Fatalf("RefundSandboxPurchase: %v", err)
	}
	if refunded.Status != wallet.SandboxPurchaseRefunded {
		t.Fatalf("expected refunded status, got %q", refunded.Status)
	}
	if len(repo.debitCalls) != 1 || repo.debitCalls[0].EntryType != wallet.EntrySandboxRefundReversal {
		t.Fatalf("expected 1 EntrySandboxRefundReversal debit, got %+v", repo.debitCalls)
	}
	if len(fakePix.Refunds) != 1 {
		t.Fatalf("expected 1 pix.Refund call, got %d", len(fakePix.Refunds))
	}
}

func TestRefundSandboxPurchaseRejectedAfterUse(t *testing.T) {
	repo := newStubRepo()
	purchases := newStubSandboxPurchaseRepo()
	fakePix := pix.NewFake()
	svc := newSandboxSvc(repo, purchases, fakePix)

	p, _, err := svc.PurchaseSandboxDirect(context.Background(), "u1", "pack_100", "idem-7")
	if err != nil {
		t.Fatalf("PurchaseSandboxDirect: %v", err)
	}
	fakePix.StageCharge(p.PurchaseID, 100, pix.ChargeCompleted, "", "e2e-3")
	if err := svc.ConfirmSandboxPurchase(context.Background(), p.PurchaseID, false); err != nil {
		t.Fatalf("ConfirmSandboxPurchase: %v", err)
	}
	repo.anyDebitSince = true // simulates an intervening DebitSandbox spend

	_, err = svc.RefundSandboxPurchase(context.Background(), "u1", p.PurchaseID, "idem-refund-2")
	pr, ok := errors.AsType[*problem.Problem](err)
	if !ok || pr.Type != problem.TypeSandboxPurchaseUsed {
		t.Fatalf("expected sandbox-purchase-used, got %v", err)
	}
	if len(fakePix.Refunds) != 0 {
		t.Fatalf("expected no refund attempt once used, got %d", len(fakePix.Refunds))
	}
}

// TestPurchaseSandboxDirectAndRefundNeverTouchRealOrGame is the executable
// form of plan §9.1's argument — same posture as the existing
// TestSandboxPurchaseNeverDebitsRealWallet regression test.
func TestPurchaseSandboxDirectAndRefundNeverTouchRealOrGame(t *testing.T) {
	repo := newStubRepo()
	purchases := newStubSandboxPurchaseRepo()
	fakePix := pix.NewFake()
	svc := newSandboxSvc(repo, purchases, fakePix)

	p, _, err := svc.PurchaseSandboxDirect(context.Background(), "u1", "pack_100", "idem-8")
	if err != nil {
		t.Fatalf("PurchaseSandboxDirect: %v", err)
	}
	fakePix.StageCharge(p.PurchaseID, 100, pix.ChargeCompleted, "", "e2e-4")
	if err := svc.ConfirmSandboxPurchase(context.Background(), p.PurchaseID, false); err != nil {
		t.Fatalf("ConfirmSandboxPurchase: %v", err)
	}
	repo.anyDebitSince = false
	if _, err := svc.RefundSandboxPurchase(context.Background(), "u1", p.PurchaseID, "idem-refund-3"); err != nil {
		t.Fatalf("RefundSandboxPurchase: %v", err)
	}

	if repo.transferCalled {
		t.Fatal("direct sandbox purchase must never call ringTransfer/Transfer")
	}
	for _, m := range repo.creditCalls {
		if m.WalletID == repo.real.WalletID || m.WalletID == repo.game.WalletID {
			t.Fatalf("credit touched real/game wallet: %+v", m)
		}
	}
	for _, m := range repo.debitCalls {
		if m.WalletID == repo.real.WalletID || m.WalletID == repo.game.WalletID {
			t.Fatalf("debit touched real/game wallet: %+v", m)
		}
	}
}
