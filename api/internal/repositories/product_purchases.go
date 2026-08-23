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

// ProductPurchaseRepository owns persistence for the generic PIX product-sale
// flow (docs/specs/2026-08-12-product-purchase-skus.md) — its own
// table/repository, decoupled from WalletRepository and from
// SandboxPurchaseRepository: this is a sale with no ledger effect at all.
type ProductPurchaseRepository struct {
	purchases Base
}

func NewProductPurchaseRepository(db *dynamodb.Client, cfg *config.Config) *ProductPurchaseRepository {
	return &ProductPurchaseRepository{purchases: NewBase(db, cfg, wallet.TableProductPurchases)}
}

var ErrProductPurchaseExists = errors.New("repositories: product purchase already exists")

func (r *ProductPurchaseRepository) PutIfAbsent(ctx context.Context, p *wallet.ProductPurchase) error {
	av, err := Encode(p)
	if err != nil {
		return err
	}
	item := r.purchases.BuildPutTxItemIfAbsent(av)
	if err := r.purchases.TransactWrite(ctx, []types.TransactWriteItem{item}); err != nil {
		if IsConditionFailed(err) {
			return ErrProductPurchaseExists
		}
		return err
	}
	return nil
}

func (r *ProductPurchaseRepository) Get(ctx context.Context, purchaseID string) (*wallet.ProductPurchase, error) {
	item, err := r.purchases.GetItem(ctx, purchaseID)
	if err != nil || item == nil {
		return nil, err
	}
	return Decode[wallet.ProductPurchase](item)
}

// ListByUser returns newest-first generic product purchases for exactly one
// owner through gsi_user. It includes M2M-opened purchases because the user,
// not the requesting client, owns the history shown in their wallet.
func (r *ProductPurchaseRepository) ListByUser(ctx context.Context, userID string, limit int, startKey map[string]types.AttributeValue) (*Page[wallet.ProductPurchase], error) {
	result, err := r.purchases.Query(ctx, QueryOpts{
		PK: userID, PKField: attributeUserID, SKField: attributeCreatedAt,
		IndexName: wallet.GSIUser, ScanIndexForward: false,
		Limit: limit, ExclusiveStartKey: startKey,
	})
	if err != nil {
		return nil, err
	}
	return DecodePage[wallet.ProductPurchase](result)
}

const (
	productStatusConditionExpression = "#status = :from"
	productStatusUpdateExpression    = "SET #status = :to, e2e_id = :e2e, " + attributeUpdatedAt + " = :now"
)

func productStatusValues(fromStatus, toStatus, e2eID string) (map[string]string, map[string]types.AttributeValue) {
	return map[string]string{"#status": "status"}, map[string]types.AttributeValue{
		":from": &types.AttributeValueMemberS{Value: fromStatus},
		":to":   &types.AttributeValueMemberS{Value: toStatus},
		":e2e":  &types.AttributeValueMemberS{Value: e2eID},
		":now":  &types.AttributeValueMemberS{Value: NowStr()},
	}
}

// TransitionStatus is a generic conditional transition — used for both
// pending→confirmed and confirmed→refunded, since neither has a companion
// ledger transaction to combine with (that's the whole simplification versus
// SandboxPurchaseRepository's BuildConfirmTx/BuildRefundClaimTx). e2eID is
// persisted on every transition (mirroring depositStatusTransition) so
// pending→confirmed is the one write that ever records it — confirmed→refunded
// just re-writes the value TransitionStatus itself already stored.
func (r *ProductPurchaseRepository) TransitionStatus(ctx context.Context, purchaseID, fromStatus, toStatus, e2eID string) (bool, error) {
	names, values := productStatusValues(fromStatus, toStatus, e2eID)
	_, err := r.purchases.UpdateItemRaw(ctx, &dynamodb.UpdateItemInput{
		Key:                       map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: purchaseID}},
		UpdateExpression:          aws.String(productStatusUpdateExpression),
		ConditionExpression:       aws.String(productStatusConditionExpression),
		ExpressionAttributeNames:  names,
		ExpressionAttributeValues: values,
	})
	if IsConditionFailed(err) {
		return false, nil
	}
	return err == nil, err
}

func (r *ProductPurchaseRepository) Update(ctx context.Context, purchaseID string, updates map[string]any) error {
	return UpdateItemWithTimestamp(ctx, r.purchases, purchaseID, updates)
}

func (r *ProductPurchaseRepository) ListPendingOlderThan(ctx context.Context, cutoff time.Time, limit int) ([]wallet.ProductPurchase, error) {
	return r.listByGSIOlderThan(ctx, wallet.GSIProductPurchaseStatus, "status", wallet.ProductPurchasePending, cutoff, limit)
}

func (r *ProductPurchaseRepository) ListWebhookFailedOlderThan(ctx context.Context, cutoff time.Time, limit int) ([]wallet.ProductPurchase, error) {
	return r.listByGSIOlderThan(ctx, wallet.GSIProductPurchaseWebhookStatus, "webhook_status", wallet.WebhookFailed, cutoff, limit)
}

func (r *ProductPurchaseRepository) listByGSIOlderThan(ctx context.Context, gsiName, attr, value string, cutoff time.Time, limit int) ([]wallet.ProductPurchase, error) {
	res, err := r.purchases.QueryGSI(ctx, gsiName, attr, value, limit, nil)
	if err != nil {
		return nil, err
	}
	out := make([]wallet.ProductPurchase, 0, len(res.Items))
	for _, it := range res.Items {
		p, err := Decode[wallet.ProductPurchase](it)
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
