//go:build integration

package integration_test

import (
	"context"
	"testing"

	"gopkg.aoctech.app/wallet/api/internal/config"
	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/repositories"
)

func TestPurchaseHistoryIsOwnershipScopedNewestFirstAndPaginated(t *testing.T) {
	ctx := context.Background()
	cfg := &config.Config{TablePrefix: tablePrefix}
	sandboxRepo := repositories.NewSandboxPurchaseRepository(db, cfg)
	productRepo := repositories.NewProductPurchaseRepository(db, cfg)

	sandboxRows := []wallet.SandboxPurchase{
		{PurchaseID: "history-sandbox-old", UserID: "history-user", SKU: "pack_100", Status: wallet.SandboxPurchaseConfirmed, CreatedAt: "2026-08-10T10:00:00Z", UpdatedAt: "2026-08-10T10:00:00Z"},
		{PurchaseID: "history-sandbox-new", UserID: "history-user", SKU: "pack_500", Status: wallet.SandboxPurchaseConfirmed, CreatedAt: "2026-08-11T10:00:00Z", UpdatedAt: "2026-08-11T10:00:00Z"},
		{PurchaseID: "history-sandbox-other", UserID: "another-user", SKU: "pack_1000", Status: wallet.SandboxPurchaseConfirmed, CreatedAt: "2026-08-12T10:00:00Z", UpdatedAt: "2026-08-12T10:00:00Z"},
	}
	for i := range sandboxRows {
		if err := sandboxRepo.PutIfAbsent(ctx, &sandboxRows[i]); err != nil {
			t.Fatalf("put sandbox purchase: %v", err)
		}
	}

	first, err := sandboxRepo.ListByUser(ctx, "history-user", 1, nil)
	if err != nil {
		t.Fatalf("first sandbox page: %v", err)
	}
	if len(first.Items) != 1 || first.Items[0].PurchaseID != "history-sandbox-new" || len(first.LastEvaluatedKey) == 0 {
		t.Fatalf("first sandbox page = %+v, want newest row and cursor", first)
	}
	second, err := sandboxRepo.ListByUser(ctx, "history-user", 1, first.LastEvaluatedKey)
	if err != nil {
		t.Fatalf("second sandbox page: %v", err)
	}
	if len(second.Items) != 1 || second.Items[0].PurchaseID != "history-sandbox-old" {
		t.Fatalf("second sandbox page = %+v, want older owned row", second)
	}

	product := &wallet.ProductPurchase{
		PurchaseID: "history-product", UserID: "history-user", SKU: "poker_reaction_fire",
		Status: wallet.ProductPurchaseConfirmed, CreatedAt: "2026-08-12T12:00:00Z", UpdatedAt: "2026-08-12T12:00:00Z",
	}
	if err := productRepo.PutIfAbsent(ctx, product); err != nil {
		t.Fatalf("put product purchase: %v", err)
	}
	products, err := productRepo.ListByUser(ctx, "history-user", 10, nil)
	if err != nil {
		t.Fatalf("product history: %v", err)
	}
	if len(products.Items) != 1 || products.Items[0].PurchaseID != product.PurchaseID {
		t.Fatalf("product history = %+v, want owned purchase", products.Items)
	}
}
