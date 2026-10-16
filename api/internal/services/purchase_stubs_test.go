package services

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/repositories"
)

// The purchases are read-only now (the sale moved to Poker and Billing): these stubs hold the old
// rows account deletion still has to look at.
type stubSandboxPurchaseRepo struct {
	purchases map[string]*wallet.SandboxPurchase
}

func newStubSandboxPurchaseRepo() *stubSandboxPurchaseRepo {
	return &stubSandboxPurchaseRepo{purchases: map[string]*wallet.SandboxPurchase{}}
}

func (r *stubSandboxPurchaseRepo) ListByUser(_ context.Context, userID string, limit int, _ map[string]types.AttributeValue) (*repositories.Page[wallet.SandboxPurchase], error) {
	items := make([]wallet.SandboxPurchase, 0, limit)
	for _, p := range r.purchases {
		if p.UserID == userID && len(items) < limit {
			items = append(items, *p)
		}
	}
	return &repositories.Page[wallet.SandboxPurchase]{Items: items}, nil
}

type stubProductPurchaseRepo struct {
	purchases map[string]*wallet.ProductPurchase
}

func newStubProductPurchaseRepo() *stubProductPurchaseRepo {
	return &stubProductPurchaseRepo{purchases: map[string]*wallet.ProductPurchase{}}
}

func (r *stubProductPurchaseRepo) ListByUser(_ context.Context, userID string, limit int, _ map[string]types.AttributeValue) (*repositories.Page[wallet.ProductPurchase], error) {
	items := make([]wallet.ProductPurchase, 0, limit)
	for _, p := range r.purchases {
		if p.UserID == userID && len(items) < limit {
			items = append(items, *p)
		}
	}
	return &repositories.Page[wallet.ProductPurchase]{Items: items}, nil
}
