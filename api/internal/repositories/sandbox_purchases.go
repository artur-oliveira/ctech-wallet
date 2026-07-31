package repositories

import (
	"context"
	"errors"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
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

const (
	sandboxStatusConditionExpression = "#status = :from"
	sandboxStatusUpdateExpression    = "SET #status = :to, " + attributeUpdatedAt + " = :now"
	sandboxConfirmUpdateExpression   = "SET #status = :to, e2e_id = :e2e, credit_sk = :credit, " + attributeUpdatedAt + " = :now"
)

func sandboxStatusValues(fromStatus, toStatus string) (map[string]string, map[string]types.AttributeValue) {
	return map[string]string{"#status": "status"}, map[string]types.AttributeValue{
		":from": &types.AttributeValueMemberS{Value: fromStatus},
		":to":   &types.AttributeValueMemberS{Value: toStatus},
		":now":  &types.AttributeValueMemberS{Value: NowStr()},
	}
}

// BuildConfirmTx composes the purchase pending→confirmed transition with the
// sandbox ledger credit transaction.
func (r *SandboxPurchaseRepository) BuildConfirmTx(purchaseID, e2eID, creditSK string) types.TransactWriteItem {
	names, values := sandboxStatusValues(wallet.SandboxPurchasePending, wallet.SandboxPurchaseConfirmed)
	values[":e2e"] = &types.AttributeValueMemberS{Value: e2eID}
	values[":credit"] = &types.AttributeValueMemberS{Value: creditSK}
	return r.purchases.BuildRawUpdateTxItem(purchaseID, nil, sandboxConfirmUpdateExpression, sandboxStatusConditionExpression, names, values)
}

// BuildRefundClaimTx makes credit revocation and the durable refund_pending
// state one atomic commit.
func (r *SandboxPurchaseRepository) BuildRefundClaimTx(purchaseID string) types.TransactWriteItem {
	names, values := sandboxStatusValues(wallet.SandboxPurchaseConfirmed, wallet.SandboxPurchaseRefundPending)
	return r.purchases.BuildRawUpdateTxItem(purchaseID, nil, sandboxStatusUpdateExpression, sandboxStatusConditionExpression, names, values)
}

func (r *SandboxPurchaseRepository) TransitionStatus(ctx context.Context, purchaseID, fromStatus, toStatus string) (bool, error) {
	names, values := sandboxStatusValues(fromStatus, toStatus)
	_, err := r.purchases.UpdateItemRaw(ctx, &dynamodb.UpdateItemInput{
		Key:                       map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: purchaseID}},
		UpdateExpression:          aws.String(sandboxStatusUpdateExpression),
		ConditionExpression:       aws.String(sandboxStatusConditionExpression),
		ExpressionAttributeNames:  names,
		ExpressionAttributeValues: values,
	})
	if IsConditionFailed(err) {
		return false, nil
	}
	return err == nil, err
}

func (r *SandboxPurchaseRepository) Update(ctx context.Context, purchaseID string, updates map[string]any) error {
	return UpdateItemWithTimestamp(ctx, r.purchases, purchaseID, updates)
}

// ListPendingOlderThan is the sweep's work queue — the same shape as
// ListPendingDepositsOlderThan, kept as a separate method (not a shared
// helper across tables) because the two tables are deliberately decoupled.
func (r *SandboxPurchaseRepository) ListPendingOlderThan(ctx context.Context, cutoff time.Time, limit int) ([]wallet.SandboxPurchase, error) {
	return r.listByGSIOlderThan(ctx, wallet.GSISandboxPurchaseStatus, "status", wallet.SandboxPurchasePending, cutoff, limit)
}

// ListRefundPendingOlderThan is the scheduled work queue for refunds whose
// provider call or final local transition did not finish.
func (r *SandboxPurchaseRepository) ListRefundPendingOlderThan(ctx context.Context, cutoff time.Time, limit int) ([]wallet.SandboxPurchase, error) {
	return r.listByGSIOlderThan(ctx, wallet.GSISandboxPurchaseStatus, "status", wallet.SandboxPurchaseRefundPending, cutoff, limit)
}

// ListWebhookFailedOlderThan is the M2M webhook notify-back retry sweep's
// work queue — same shape as ListPendingOlderThan, different GSI/value.
func (r *SandboxPurchaseRepository) ListWebhookFailedOlderThan(ctx context.Context, cutoff time.Time, limit int) ([]wallet.SandboxPurchase, error) {
	return r.listByGSIOlderThan(ctx, wallet.GSISandboxPurchaseWebhookStatus, "webhook_status", wallet.WebhookFailed, cutoff, limit)
}

func (r *SandboxPurchaseRepository) listByGSIOlderThan(ctx context.Context, gsiName, attr, value string, cutoff time.Time, limit int) ([]wallet.SandboxPurchase, error) {
	res, err := r.purchases.QueryGSI(ctx, gsiName, attr, value, limit, nil)
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
