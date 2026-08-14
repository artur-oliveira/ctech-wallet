package services

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

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

func (r *stubSandboxPurchaseRepo) ListByUser(_ context.Context, userID string, limit int, _ map[string]types.AttributeValue) (*repositories.Page[wallet.SandboxPurchase], error) {
	items := make([]wallet.SandboxPurchase, 0, limit)
	for _, purchase := range r.purchases {
		if purchase.UserID == userID && len(items) < limit {
			items = append(items, *purchase)
		}
	}
	return &repositories.Page[wallet.SandboxPurchase]{Items: items}, nil
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
	if v, ok := updates["webhook_status"].(string); ok {
		p.WebhookStatus = v
	}
	return nil
}

func (r *stubSandboxPurchaseRepo) BuildConfirmTx(purchaseID, e2eID, creditSK string) types.TransactWriteItem {
	p := r.purchases[purchaseID]
	if p != nil && p.Status == wallet.SandboxPurchasePending {
		p.Status, p.E2EID, p.CreditSK = wallet.SandboxPurchaseConfirmed, e2eID, creditSK
	}
	return types.TransactWriteItem{}
}

func (r *stubSandboxPurchaseRepo) BuildRefundClaimTx(purchaseID string) types.TransactWriteItem {
	p := r.purchases[purchaseID]
	if p != nil && p.Status == wallet.SandboxPurchaseConfirmed {
		p.Status = wallet.SandboxPurchaseRefundPending
	}
	return types.TransactWriteItem{}
}

func (r *stubSandboxPurchaseRepo) TransitionStatus(_ context.Context, purchaseID, fromStatus, toStatus string) (bool, error) {
	p := r.purchases[purchaseID]
	if p == nil || p.Status != fromStatus {
		return false, nil
	}
	p.Status = toStatus
	return true, nil
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

func (r *stubSandboxPurchaseRepo) ListWebhookFailedOlderThan(_ context.Context, _ time.Time, _ int) ([]wallet.SandboxPurchase, error) {
	var out []wallet.SandboxPurchase
	for _, p := range r.purchases {
		if p.WebhookStatus == wallet.WebhookFailed {
			out = append(out, *p)
		}
	}
	return out, nil
}

func (r *stubSandboxPurchaseRepo) ListRefundPendingOlderThan(_ context.Context, _ time.Time, _ int) ([]wallet.SandboxPurchase, error) {
	var out []wallet.SandboxPurchase
	for _, p := range r.purchases {
		if p.Status == wallet.SandboxPurchaseRefundPending {
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

	p, charge, err := svc.PurchaseSandboxDirect(context.Background(), "u1", "pack_100", "idem-1", "")
	if err != nil {
		t.Fatalf("PurchaseSandboxDirect: %v", err)
	}
	if p.AmountExpected != 100 || p.CreditsGranted != 100*wallet.SandboxCreditsPerCent {
		t.Fatalf("unexpected purchase: %+v", p)
	}
	if charge.Amount != 100 {
		t.Fatalf("unexpected charge amount: %d", charge.Amount)
	}
	if len(fakePix.CreatedCharges) != 1 {
		t.Fatalf("expected 1 CreateCharge call, got %d", len(fakePix.CreatedCharges))
	}
	if got := fakePix.CreatedCharges[0]; !regexp.MustCompile(`^[a-zA-Z0-9]{26,35}$`).MatchString(got) {
		t.Fatalf("Inter txid %q does not satisfy [a-zA-Z0-9]{26,35}", got)
	}
	if fakePix.CreatedCharges[0] != p.PurchaseID {
		t.Fatalf("purchase id and Inter txid diverged: %q vs %q", p.PurchaseID, fakePix.CreatedCharges[0])
	}
}

func TestPurchaseSandboxDirectRejectsUnknownSKU(t *testing.T) {
	repo := newStubRepo()
	purchases := newStubSandboxPurchaseRepo()
	svc := newSandboxSvc(repo, purchases, pix.NewFake())

	_, _, err := svc.PurchaseSandboxDirect(context.Background(), "u1", "not-a-sku", "idem-2", "")
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

	first, _, err := svc.PurchaseSandboxDirect(context.Background(), "u1", "pack_100", "idem-3", "")
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	second, _, err := svc.PurchaseSandboxDirect(context.Background(), "u1", "pack_100", "idem-3", "")
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

func TestPurchaseSandboxDirectRejectsSameKeyDifferentSKU(t *testing.T) {
	repo := newStubRepo()
	purchases := newStubSandboxPurchaseRepo()
	svc := newSandboxSvc(repo, purchases, pix.NewFake())
	if _, _, err := svc.PurchaseSandboxDirect(context.Background(), "u1", "pack_100", "idem-conflict", "client-a"); err != nil {
		t.Fatalf("first call: %v", err)
	}
	_, _, err := svc.PurchaseSandboxDirect(context.Background(), "u1", "pack_500", "idem-conflict", "client-a")
	p, ok := errors.AsType[*problem.Problem](err)
	if !ok || p.Type != problem.TypeIdempotencyConflict {
		t.Fatalf("expected idempotency conflict, got %v", err)
	}
}

func TestConfirmSandboxPurchaseCreditsSandboxWallet(t *testing.T) {
	repo := newStubRepo()
	purchases := newStubSandboxPurchaseRepo()
	fakePix := pix.NewFake()
	svc := newSandboxSvc(repo, purchases, fakePix)

	p, _, err := svc.PurchaseSandboxDirect(context.Background(), "u1", "pack_100", "idem-4", "")
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
	if repo.creditCalls[0].Amount != 100*wallet.SandboxCreditsPerCent {
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

	p, _, err := svc.PurchaseSandboxDirect(context.Background(), "u1", "pack_100", "idem-5", "")
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

	p, _, err := svc.PurchaseSandboxDirect(context.Background(), "u1", "pack_100", "idem-6", "")
	if err != nil {
		t.Fatalf("PurchaseSandboxDirect: %v", err)
	}
	fakePix.StageCharge(p.PurchaseID, 100, pix.ChargeCompleted, "", "e2e-2")
	if err := svc.ConfirmSandboxPurchase(context.Background(), p.PurchaseID, false); err != nil {
		t.Fatalf("ConfirmSandboxPurchase: %v", err)
	}
	repo.anyDebitSince = false

	refunded, err := svc.RefundSandboxPurchase(context.Background(), "u1", p.PurchaseID, "idem-refund-1", "")
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

	p, _, err := svc.PurchaseSandboxDirect(context.Background(), "u1", "pack_100", "idem-7", "")
	if err != nil {
		t.Fatalf("PurchaseSandboxDirect: %v", err)
	}
	fakePix.StageCharge(p.PurchaseID, 100, pix.ChargeCompleted, "", "e2e-3")
	if err := svc.ConfirmSandboxPurchase(context.Background(), p.PurchaseID, false); err != nil {
		t.Fatalf("ConfirmSandboxPurchase: %v", err)
	}
	repo.anyDebitSince = true // simulates an intervening DebitSandbox spend

	_, err = svc.RefundSandboxPurchase(context.Background(), "u1", p.PurchaseID, "idem-refund-2", "")
	pr, ok := errors.AsType[*problem.Problem](err)
	if !ok || pr.Type != problem.TypeSandboxPurchaseUsed {
		t.Fatalf("expected sandbox-purchase-used, got %v", err)
	}
	if len(fakePix.Refunds) != 0 {
		t.Fatalf("expected no refund attempt once used, got %d", len(fakePix.Refunds))
	}
}

// TestM2MPurchaseSandboxDirectNamespacesPurchaseID guards the SEC-08-style
// idempotency reservation: a user-direct purchase and an M2M-opened purchase
// for the same (userID, idemKey) must never collide on the same DynamoDB row,
// and two different M2M clients using the same (userID, idemKey) must not
// collide with each other either.
func TestM2MPurchaseSandboxDirectNamespacesPurchaseID(t *testing.T) {
	repo := newStubRepo()
	purchases := newStubSandboxPurchaseRepo()
	svc := newSandboxSvc(repo, purchases, pix.NewFake())

	direct, _, err := svc.PurchaseSandboxDirect(context.Background(), "u1", "pack_100", "shared-key", "")
	if err != nil {
		t.Fatalf("user-direct PurchaseSandboxDirect: %v", err)
	}
	poker, _, err := svc.PurchaseSandboxDirect(context.Background(), "u1", "pack_100", "shared-key", "poker")
	if err != nil {
		t.Fatalf("m2m PurchaseSandboxDirect: %v", err)
	}
	domino, _, err := svc.PurchaseSandboxDirect(context.Background(), "u1", "pack_100", "shared-key", "domino")
	if err != nil {
		t.Fatalf("m2m PurchaseSandboxDirect (second client): %v", err)
	}
	if direct.PurchaseID == poker.PurchaseID || poker.PurchaseID == domino.PurchaseID || direct.PurchaseID == domino.PurchaseID {
		t.Fatalf("expected disjoint purchase IDs, got direct=%q poker=%q domino=%q", direct.PurchaseID, poker.PurchaseID, domino.PurchaseID)
	}
	validInterTxID := regexp.MustCompile(`^sbxp[a-f0-9]{31}$`)
	for _, purchase := range []*wallet.SandboxPurchase{direct, poker, domino} {
		if !validInterTxID.MatchString(purchase.PurchaseID) {
			t.Fatalf("purchase ID %q is not an Inter-compatible sandbox txid", purchase.PurchaseID)
		}
	}
}

// TestM2MSandboxPurchaseOwnershipIsolation guards SEC-07-style ownership: an
// M2M client may only read/refund purchases it itself opened — never another
// client's, never a user-direct purchase.
func TestM2MSandboxPurchaseOwnershipIsolation(t *testing.T) {
	repo := newStubRepo()
	purchases := newStubSandboxPurchaseRepo()
	fakePix := pix.NewFake()
	svc := newSandboxSvc(repo, purchases, fakePix)

	p, _, err := svc.PurchaseSandboxDirect(context.Background(), "u1", "pack_100", "idem-poker-1", "poker")
	if err != nil {
		t.Fatalf("PurchaseSandboxDirect: %v", err)
	}
	fakePix.StageCharge(p.PurchaseID, 100, pix.ChargeCompleted, "", "e2e-poker-1")
	if err := svc.ConfirmSandboxPurchase(context.Background(), p.PurchaseID, false); err != nil {
		t.Fatalf("ConfirmSandboxPurchase: %v", err)
	}

	if _, err := svc.GetSandboxPurchase(context.Background(), p.PurchaseID, "domino"); err == nil {
		t.Fatal("expected a different client's GET to be rejected as not-found")
	} else if pr, ok := errors.AsType[*problem.Problem](err); !ok || pr.Type != problem.TypeNotFound {
		t.Fatalf("expected not-found, got %v", err)
	}
	if got, err := svc.GetSandboxPurchase(context.Background(), p.PurchaseID, "poker"); err != nil || got.PurchaseID != p.PurchaseID {
		t.Fatalf("expected the owning client's GET to succeed, got %+v, err=%v", got, err)
	}

	if _, err := svc.RefundSandboxPurchase(context.Background(), "u1", p.PurchaseID, "idem-refund-poker-1", "domino"); err == nil {
		t.Fatal("expected a different client's refund to be rejected as not-found")
	} else if pr, ok := errors.AsType[*problem.Problem](err); !ok || pr.Type != problem.TypeNotFound {
		t.Fatalf("expected not-found, got %v", err)
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

	p, _, err := svc.PurchaseSandboxDirect(context.Background(), "u1", "pack_100", "idem-8", "")
	if err != nil {
		t.Fatalf("PurchaseSandboxDirect: %v", err)
	}
	fakePix.StageCharge(p.PurchaseID, 100, pix.ChargeCompleted, "", "e2e-4")
	if err := svc.ConfirmSandboxPurchase(context.Background(), p.PurchaseID, false); err != nil {
		t.Fatalf("ConfirmSandboxPurchase: %v", err)
	}
	repo.anyDebitSince = false
	if _, err := svc.RefundSandboxPurchase(context.Background(), "u1", p.PurchaseID, "idem-refund-3", ""); err != nil {
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
