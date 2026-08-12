package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/pix"
	"gopkg.aoctech.app/wallet/api/internal/repositories"
)

type stubProductPurchaseRepo struct {
	purchases map[string]*wallet.ProductPurchase
}

func newStubProductPurchaseRepo() *stubProductPurchaseRepo {
	return &stubProductPurchaseRepo{purchases: map[string]*wallet.ProductPurchase{}}
}

func (r *stubProductPurchaseRepo) PutIfAbsent(_ context.Context, p *wallet.ProductPurchase) error {
	if _, ok := r.purchases[p.PurchaseID]; ok {
		return repositories.ErrProductPurchaseExists
	}
	cp := *p
	r.purchases[p.PurchaseID] = &cp
	return nil
}

func (r *stubProductPurchaseRepo) Get(_ context.Context, purchaseID string) (*wallet.ProductPurchase, error) {
	return r.purchases[purchaseID], nil
}

func (r *stubProductPurchaseRepo) TransitionStatus(_ context.Context, purchaseID, fromStatus, toStatus string) (bool, error) {
	p := r.purchases[purchaseID]
	if p == nil || p.Status != fromStatus {
		return false, nil
	}
	p.Status = toStatus
	return true, nil
}

func (r *stubProductPurchaseRepo) Update(_ context.Context, purchaseID string, updates map[string]any) error {
	p := r.purchases[purchaseID]
	if p == nil {
		return nil
	}
	if v, ok := updates["webhook_status"].(string); ok {
		p.WebhookStatus = v
	}
	if v, ok := updates["created_at"].(string); ok {
		p.CreatedAt = v
	}
	return nil
}

func (r *stubProductPurchaseRepo) ListPendingOlderThan(_ context.Context, cutoff time.Time, _ int) ([]wallet.ProductPurchase, error) {
	var out []wallet.ProductPurchase
	for _, p := range r.purchases {
		createdAt, err := time.Parse(time.RFC3339Nano, p.CreatedAt)
		if err != nil {
			continue
		}
		if p.Status == wallet.ProductPurchasePending && createdAt.Before(cutoff) {
			out = append(out, *p)
		}
	}
	return out, nil
}

func (r *stubProductPurchaseRepo) ListWebhookFailedOlderThan(_ context.Context, _ time.Time, _ int) ([]wallet.ProductPurchase, error) {
	var out []wallet.ProductPurchase
	for _, p := range r.purchases {
		if p.WebhookStatus == wallet.WebhookFailed {
			out = append(out, *p)
		}
	}
	return out, nil
}

func newTestWalletServiceForProduct() (*WalletService, *stubProductPurchaseRepo, *pix.FakePixClient) {
	repo := newStubProductPurchaseRepo()
	fakePix := pix.NewFake()
	svc := NewWalletService(nil, nil, nil, nil, fakePix, nil)
	svc.SetProductPurchases(repo)
	return svc, repo, fakePix
}

func TestPurchaseProductDirectIsIdempotent(t *testing.T) {
	svc, _, _ := newTestWalletServiceForProduct()
	ctx := context.Background()

	p1, charge1, err := svc.PurchaseProductDirect(ctx, "user-1", "poker_reaction_cold", "idem-1", "poker")
	if err != nil {
		t.Fatalf("first purchase: %v", err)
	}
	if p1.Status != wallet.ProductPurchasePending || p1.AmountExpected != 100 {
		t.Fatalf("unexpected purchase: %+v", p1)
	}

	p2, charge2, err := svc.PurchaseProductDirect(ctx, "user-1", "poker_reaction_cold", "idem-1", "poker")
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if p2.PurchaseID != p1.PurchaseID {
		t.Fatalf("replay must return the same purchase_id: %s vs %s", p2.PurchaseID, p1.PurchaseID)
	}
	if charge1.QRCode != charge2.QRCode {
		t.Fatal("replay must return the (re-queried) same charge")
	}
}

func TestPurchaseProductDirectUnknownSKU(t *testing.T) {
	svc, _, _ := newTestWalletServiceForProduct()
	_, _, err := svc.PurchaseProductDirect(context.Background(), "user-1", "no-such-sku", "idem-1", "poker")
	if err == nil {
		t.Fatal("expected an error for unknown sku")
	}
	var target interface{ Error() string }
	if !errors.As(err, &target) {
		t.Fatalf("expected a wrapped error, got %v", err)
	}
}
