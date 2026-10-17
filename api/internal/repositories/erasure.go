package repositories

import (
	"context"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/wallet/api/internal/config"
	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
)

// Attribute names the purge removes or sets. Mirrored by cdk/lib/constants.ts
// AUDIT_ANONYMIZABLE_ATTRIBUTES for the audit pair.
const (
	attrPayerCPF       = "payer_cpf"
	attrPayerName      = "payer_name"
	attrPixKey         = "pix_key"
	attrSelfExclusion  = "self_exclusion"
	attrGameLimits     = "game_limits"
	attrErasedAt       = "erased_at"
	attrAuditIP        = "ip"
	attrAuditUserAgent = "user_agent"
	attrTTL            = "ttl"
	attrUserID         = "user_id"
)

const erasurePageSize = 100

// ErasureRepository is the persistence side of the account-deletion purge
// (docs/plans/2026-10-07-account-deletion-participant.md). Every write is
// idempotent: the purge may run any number of times or stop anywhere.
type ErasureRepository struct {
	deposits    Base
	withdrawals Base
	holds       Base
	users       Base
	audit       Base
	retention   Base
}

func NewErasureRepository(db *dynamodb.Client, cfg *config.Config) *ErasureRepository {
	return &ErasureRepository{
		deposits:    NewBase(db, cfg, wallet.TablePixDeposits),
		withdrawals: NewBase(db, cfg, wallet.TableWithdrawals),
		holds:       NewBase(db, cfg, wallet.TableHolds),
		users:       NewBase(db, cfg, wallet.TableUsers),
		audit:       NewBase(db, cfg, wallet.TableAudit),
		retention:   NewBase(db, cfg, wallet.TableErasureRetention),
	}
}

// allByUser pages through a table's gsi_user partition for one user (R21): a
// key-condition Query, so the cost is the user's own rows, whatever the table
// holds. gsi_user is sorted by created_at.
func allByUser[T any](ctx context.Context, b Base, userID string) ([]T, error) {
	var out []T
	var start map[string]types.AttributeValue
	for {
		res, err := b.QueryGSI(ctx, wallet.GSIUser, attrUserID, userID, erasurePageSize, start)
		if err != nil {
			return nil, err
		}
		for _, it := range res.Items {
			v, err := Decode[T](it)
			if err != nil {
				return nil, err
			}
			out = append(out, *v)
		}
		if len(res.LastEvaluatedKey) == 0 {
			return out, nil
		}
		start = res.LastEvaluatedKey
	}
}

// DepositsForUser returns every PIX deposit row of userID, any status.
func (r *ErasureRepository) DepositsForUser(ctx context.Context, userID string) ([]wallet.PixDeposit, error) {
	return allByUser[wallet.PixDeposit](ctx, r.deposits, userID)
}

// WithdrawalsForUser returns every withdrawal row of userID, any status.
func (r *ErasureRepository) WithdrawalsForUser(ctx context.Context, userID string) ([]wallet.Withdrawal, error) {
	return allByUser[wallet.Withdrawal](ctx, r.withdrawals, userID)
}

// HoldsForUser returns every hold row of userID, any status (personal-data export).
func (r *ErasureRepository) HoldsForUser(ctx context.Context, userID string) ([]wallet.Hold, error) {
	return allByUser[wallet.Hold](ctx, r.holds, userID)
}

// OpenHoldsForUser returns userID's holds still `held`. A user has few holds,
// so the status is filtered over the user's own partition, not the shared
// `held` partition of gsi_hold_status.
func (r *ErasureRepository) OpenHoldsForUser(ctx context.Context, userID string) ([]wallet.Hold, error) {
	all, err := allByUser[wallet.Hold](ctx, r.holds, userID)
	if err != nil {
		return nil, err
	}
	open := all[:0]
	for _, h := range all {
		if h.Status == wallet.HoldHeld {
			open = append(open, h)
		}
	}
	return open, nil
}

// RetentionSK is the retention sort key of one source row.
func RetentionSK(source, sourceKey string) string { return source + "#" + sourceKey }

type retainedRecord struct {
	PK          string `dynamodbav:"pk"`
	SK          string `dynamodbav:"sk"`
	Source      string `dynamodbav:"source"`
	Item        any    `dynamodbav:"item"`
	RetainedAt  string `dynamodbav:"retained_at"`
	RetainUntil string `dynamodbav:"retain_until"`
	TTL         int64  `dynamodbav:"ttl"`
}

// Retain copies item into the write-only retention table, write-once: a
// second call for the same source row is a no-op, so a rerun after the
// original was stripped can never overwrite the full copy with the stripped
// one. until is both retain_until and the DynamoDB TTL.
func (r *ErasureRepository) Retain(ctx context.Context, sub, source, sourceKey string, item any, until time.Time) error {
	av, err := attributevalue.MarshalMap(retainedRecord{
		PK: wallet.RetentionSubPrefix + sub, SK: RetentionSK(source, sourceKey), Source: source, Item: item,
		RetainedAt: NowStr(), RetainUntil: until.UTC().Format(time.RFC3339), TTL: until.Unix(),
	})
	if err != nil {
		return err
	}
	_, err = r.retention.PutItemRaw(ctx, &dynamodb.PutItemInput{
		Item:                av,
		ConditionExpression: aws.String("attribute_not_exists(pk)"),
	})
	if IsConditionFailed(err) {
		return nil
	}
	return err
}

// StripDepositPII removes the payer identity from a deposit row; the money
// fields stay. Missing row: no-op.
func (r *ErasureRepository) StripDepositPII(ctx context.Context, txid string) error {
	_, err := r.deposits.UpdateItem(ctx, txid, nil, map[string]any{
		attrPayerCPF: nil, attrPayerName: nil, attrErasedAt: NowStr(),
	})
	return err
}

// StripWithdrawalPII removes the destination PIX key from a withdrawal row.
func (r *ErasureRepository) StripWithdrawalPII(ctx context.Context, withdrawalID string) error {
	_, err := r.withdrawals.UpdateItem(ctx, withdrawalID, nil, map[string]any{
		attrPixKey: nil, attrErasedAt: NowStr(),
	})
	return err
}

// EraseUserProfile removes the responsible-gambling state from the user row
// and stamps erased_at. Consent versions and timestamps stay (proof of
// consent). Returns false when the user never had a row.
func (r *ErasureRepository) EraseUserProfile(ctx context.Context, userID string) (bool, error) {
	return r.users.UpdateItem(ctx, userID, nil, map[string]any{
		attrSelfExclusion: nil, attrGameLimits: nil, attrGameDepositCounters: nil, attrRealDailyCounters: nil, attrErasedAt: NowStr(),
	})
}

// AnonymizeAudit removes ip and user_agent from every audit row of userID and
// returns how many rows it changed. It touches nothing else: IAM allows
// UpdateItem on wallet_audit for exactly {pk, sk, ip, user_agent}.
func (r *ErasureRepository) AnonymizeAudit(ctx context.Context, userID string) (int, error) {
	n := 0
	var start map[string]types.AttributeValue
	for {
		res, err := r.audit.Query(ctx, QueryOpts{PK: userID, Limit: erasurePageSize, ExclusiveStartKey: start})
		if err != nil {
			return n, err
		}
		for _, it := range res.Items {
			e, err := Decode[wallet.AuditEvent](it)
			if err != nil {
				return n, err
			}
			if e.IP == "" && e.UserAgent == "" {
				continue
			}
			sk := e.SK
			if _, err := r.audit.UpdateItem(ctx, userID, &sk, map[string]any{attrAuditIP: nil, attrAuditUserAgent: nil}); err != nil {
				return n, err
			}
			n++
		}
		if len(res.LastEvaluatedKey) == 0 {
			return n, nil
		}
		start = res.LastEvaluatedKey
	}
}

type carriedExclusion struct {
	PK            string               `dynamodbav:"pk"`
	SelfExclusion wallet.SelfExclusion `dynamodbav:"self_exclusion"`
	TTL           int64                `dynamodbav:"ttl"`
}

// PutCarriedSelfExclusion keeps ex past the account's erasure (D13), keyed by
// ctech-account's CPF keyed hash, until expiresAt (DynamoDB TTL). If one is
// already stored for the same CPF, the one that lasts longer wins: a person
// can delete more than one account.
func (r *ErasureRepository) PutCarriedSelfExclusion(ctx context.Context, cpfHMAC string, ex wallet.SelfExclusion, expiresAt time.Time) error {
	av, err := attributevalue.MarshalMap(carriedExclusion{PK: wallet.SelfExclusionCarryPrefix + cpfHMAC, SelfExclusion: ex, TTL: expiresAt.Unix()})
	if err != nil {
		return err
	}
	_, err = r.users.PutItemRaw(ctx, &dynamodb.PutItemInput{
		Item:                     av,
		ConditionExpression:      aws.String("attribute_not_exists(pk) OR #ttl < :ttl"),
		ExpressionAttributeNames: map[string]string{"#ttl": attrTTL},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":ttl": &types.AttributeValueMemberN{Value: strconv.FormatInt(expiresAt.Unix(), 10)},
		},
	})
	if IsConditionFailed(err) {
		return nil
	}
	return err
}

// GetCarriedSelfExclusion returns the exclusion carried for cpfHMAC, or nil
// when there is none or it has expired (TTL deletion lags by up to days).
func (r *ErasureRepository) GetCarriedSelfExclusion(ctx context.Context, cpfHMAC string, now time.Time) (*wallet.SelfExclusion, error) {
	item, err := r.users.GetItem(ctx, wallet.SelfExclusionCarryPrefix+cpfHMAC)
	if err != nil || item == nil {
		return nil, err
	}
	c, err := Decode[carriedExclusion](item)
	if err != nil {
		return nil, err
	}
	if c.TTL <= now.Unix() {
		return nil, nil
	}
	return &c.SelfExclusion, nil
}
