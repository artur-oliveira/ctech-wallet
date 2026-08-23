//go:build integration

package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"gopkg.aoctech.app/wallet/api/internal/config"
	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/repositories"
)

func TestProductPurchaseRepositoryPutIfAbsentIsIdempotent(t *testing.T) {
	repo := repositories.NewProductPurchaseRepository(db, &config.Config{TablePrefix: tablePrefix})
	ctx := context.Background()
	p := &wallet.ProductPurchase{
		PurchaseID: "prdp-test-1", UserID: "user-1", SKU: "poker_reaction_cold",
		AmountExpected: 100, Status: wallet.ProductPurchasePending,
		CreatedAt: repositories.NowStr(), UpdatedAt: repositories.NowStr(),
	}
	if err := repo.PutIfAbsent(ctx, p); err != nil {
		t.Fatalf("first PutIfAbsent: %v", err)
	}
	if err := repo.PutIfAbsent(ctx, p); !errors.Is(err, repositories.ErrProductPurchaseExists) {
		t.Fatalf("second PutIfAbsent: got %v, want ErrProductPurchaseExists", err)
	}
	got, err := repo.Get(ctx, "prdp-test-1")
	if err != nil || got == nil || got.Status != wallet.ProductPurchasePending {
		t.Fatalf("Get: %v, %+v", err, got)
	}
}

func TestProductPurchaseRepositoryTransitionStatus(t *testing.T) {
	repo := repositories.NewProductPurchaseRepository(db, &config.Config{TablePrefix: tablePrefix})
	ctx := context.Background()
	p := &wallet.ProductPurchase{
		PurchaseID: "prdp-test-2", UserID: "user-1", SKU: "poker_reaction_cold",
		AmountExpected: 100, Status: wallet.ProductPurchasePending,
		CreatedAt: repositories.NowStr(), UpdatedAt: repositories.NowStr(),
	}
	if err := repo.PutIfAbsent(ctx, p); err != nil {
		t.Fatalf("PutIfAbsent: %v", err)
	}
	ok, err := repo.TransitionStatus(ctx, "prdp-test-2", wallet.ProductPurchasePending, wallet.ProductPurchaseConfirmed, "E2E-test-2")
	if err != nil || !ok {
		t.Fatalf("TransitionStatus: ok=%v err=%v", ok, err)
	}
	// e2e_id must survive the transition — RefundProductPurchase reads it back
	// from storage to build Inter's devolução call; an empty value there
	// produces a malformed "//devolucao/" path.
	if got, err := repo.Get(ctx, "prdp-test-2"); err != nil || got.E2EID != "E2E-test-2" {
		t.Fatalf("Get after confirm: e2e_id = %q, err = %v, want E2E-test-2", got.E2EID, err)
	}
	// Wrong `from` fails the condition and reports ok=false, not an error.
	ok, err = repo.TransitionStatus(ctx, "prdp-test-2", wallet.ProductPurchasePending, wallet.ProductPurchaseConfirmed, "E2E-test-2")
	if err != nil || ok {
		t.Fatalf("expected ok=false on stale from-status, got ok=%v err=%v", ok, err)
	}
	cutoff := time.Now().Add(time.Hour)
	rows, err := repo.ListPendingOlderThan(ctx, cutoff, 10)
	if err != nil {
		t.Fatalf("ListPendingOlderThan: %v", err)
	}
	for _, r := range rows {
		if r.PurchaseID == "prdp-test-2" {
			t.Fatal("confirmed purchase must not appear in the pending sweep list")
		}
	}
}
