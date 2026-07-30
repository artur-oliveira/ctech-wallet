package repositories

import (
	"context"
	"errors"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/wallet/api/internal/config"
	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
)

// SandboxPurchaseRepository owns persistence for the direct PIX→sandbox
// purchase flow (plan §9.1/§9.3) — deliberately its own repository/table,
// decoupled from WalletRepository's deposit/withdrawal tables: a deposit is
// custody, this is a sale.
type SandboxPurchaseRepository struct {
	purchases Base
}

func NewSandboxPurchaseRepository(db *dynamodb.Client, cfg *config.Config) *SandboxPurchaseRepository {
	return &SandboxPurchaseRepository{purchases: NewBase(db, cfg, wallet.TableSandboxPurchases)}
}

// ErrSandboxPurchaseExists means PutIfAbsent lost a race (or is a genuine
// replay) — the same purchaseID already exists. Mirrors
// repositories.ErrWithdrawalExists/ErrTransferIntentExists.
var ErrSandboxPurchaseExists = errors.New("repositories: sandbox purchase already exists")

// PutIfAbsent registers a new purchase row before any PIX charge is opened —
// SEC-08-style idempotency: a retried purchase request never opens a second
// charge (mirrors ReserveDepositIdem's own precondition).
func (r *SandboxPurchaseRepository) PutIfAbsent(ctx context.Context, p *wallet.SandboxPurchase) error {
	av, err := Encode(p)
	if err != nil {
		return err
	}
	item := r.purchases.BuildPutTxItemIfAbsent(av)
	if err := r.purchases.TransactWrite(ctx, []types.TransactWriteItem{item}); err != nil {
		if IsConditionFailed(err) {
			return ErrSandboxPurchaseExists
		}
		return err
	}
	return nil
}

func (r *SandboxPurchaseRepository) Get(ctx context.Context, purchaseID string) (*wallet.SandboxPurchase, error) {
	item, err := r.purchases.GetItem(ctx, purchaseID)
	if err != nil || item == nil {
		return nil, err
	}
	return Decode[wallet.SandboxPurchase](item)
}

func (r *SandboxPurchaseRepository) Update(ctx context.Context, purchaseID string, updates map[string]any) error {
	updates["updated_at"] = NowStr()
	_, err := r.purchases.UpdateItem(ctx, purchaseID, nil, updates)
	return err
}

// ListPendingOlderThan is the sweep's work queue — the same shape as
// ListPendingDepositsOlderThan, kept as a separate method (not a shared
// helper) because the two tables are deliberately decoupled.
func (r *SandboxPurchaseRepository) ListPendingOlderThan(ctx context.Context, cutoff time.Time, limit int) ([]wallet.SandboxPurchase, error) {
	res, err := r.purchases.QueryGSI(ctx, wallet.GSISandboxPurchaseStatus, "status", wallet.SandboxPurchasePending, limit, nil)
	if err != nil {
		return nil, err
	}
	out := make([]wallet.SandboxPurchase, 0, len(res.Items))
	for _, it := range res.Items {
		p, err := Decode[wallet.SandboxPurchase](it)
		if err != nil {
			return nil, err
		}
		createdAt, err := time.Parse(time.RFC3339Nano, p.CreatedAt)
		if err != nil {
			continue
		}
		if createdAt.Before(cutoff) {
			out = append(out, *p)
		}
	}
	return out, nil
}
