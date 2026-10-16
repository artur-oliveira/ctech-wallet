package services

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/repositories"
)

// sandboxPurchaseTTLMinutes is how long an old, unpaid Sandbox-pack charge could still have been
// paid. Poker sells through Billing now; the rows remain for the statement and for account deletion,
// which still refuses to erase a user whose old charge may yet settle.
const sandboxPurchaseTTLMinutes = depositTTLMinutes

// SandboxPurchaseRepo and ProductPurchaseRepo are the read side of the purchases the wallet used to sell.
// Nothing writes them any more (the sale moved to Poker and Billing); old rows stay readable.
type SandboxPurchaseRepo interface {
	ListByUser(ctx context.Context, userID string, limit int, startKey map[string]types.AttributeValue) (*repositories.Page[wallet.SandboxPurchase], error)
}

type ProductPurchaseRepo interface {
	ListByUser(ctx context.Context, userID string, limit int, startKey map[string]types.AttributeValue) (*repositories.Page[wallet.ProductPurchase], error)
}

func (s *WalletService) SetSandboxPurchases(r SandboxPurchaseRepo) { s.sandboxPurchases = r }
func (s *WalletService) SetProductPurchases(r ProductPurchaseRepo) { s.productPurchases = r }

// SandboxPurchaseHistory is the ownership-scoped read of a user's old Sandbox-pack purchases.
func (s *WalletService) SandboxPurchaseHistory(ctx context.Context, userID string, limit int, startKey map[string]types.AttributeValue) (*repositories.Page[wallet.SandboxPurchase], error) {
	return s.sandboxPurchases.ListByUser(ctx, userID, limit, startKey)
}

// ProductPurchaseHistory is the same for old product purchases.
func (s *WalletService) ProductPurchaseHistory(ctx context.Context, userID string, limit int, startKey map[string]types.AttributeValue) (*repositories.Page[wallet.ProductPurchase], error) {
	return s.productPurchases.ListByUser(ctx, userID, limit, startKey)
}
