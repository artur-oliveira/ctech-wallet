package repositories

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/wallet/api/internal/config"
	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
)

// UserRepository persists the wallet-side per-user state (terms acceptance).
type UserRepository struct {
	users Base
}

func NewUserRepository(db *dynamodb.Client, cfg *config.Config) *UserRepository {
	return &UserRepository{users: NewBase(db, cfg, wallet.TableUsers)}
}

// Get returns the user's wallet-side row, or nil if they have never accepted
// anything yet (no row is created until acceptance).
func (r *UserRepository) Get(ctx context.Context, userID string) (*wallet.User, error) {
	item, err := r.users.GetItem(ctx, userID)
	if err != nil || item == nil {
		return nil, err
	}
	return Decode[wallet.User](item)
}

// AcceptTerms stamps the current terms addendum version and the acceptance
// timestamp. Upsert: the row is created on first acceptance.
//
// A PARTIAL update, deliberately — NOT a whole-row Put. The row also carries the
// gambling addendum acceptance, and overwriting it wholesale would silently
// revoke that consent.
func (r *UserRepository) AcceptTerms(ctx context.Context, userID string) error {
	now := NowStr()
	return r.users.UpsertAttrs(ctx, userID, nil, map[string]any{
		"terms_addendum_version": wallet.CurrentTermsAddendumVersion,
		"terms_accepted_at":      now,
		"updated_at":             now,
	})
}

// SetSelfExclusion stores (or, with nil, removes) the user's self-exclusion.
// Partial update — the row carries independently written consent fields.
func (r *UserRepository) SetSelfExclusion(ctx context.Context, userID string, ex *wallet.SelfExclusion) error {
	var v any // untyped nil so buildUpdateExpr emits REMOVE; a typed nil would not compare equal to nil
	if ex != nil {
		v = ex
	}
	return r.users.UpsertAttrs(ctx, userID, nil, map[string]any{
		"self_exclusion": v,
		"updated_at":     NowStr(),
	})
}

// SetGameLimits stores the user's game-deposit limits (whole nested object —
// it is small and owned by exactly one writer path). Partial row update.
func (r *UserRepository) SetGameLimits(ctx context.Context, userID string, lim *wallet.GameLimits) error {
	return r.users.UpsertAttrs(ctx, userID, nil, map[string]any{
		"game_limits": lim,
		"updated_at":  NowStr(),
	})
}

// BumpDepositCounters returns a TransactWriteItem replacing the user's
// game_deposit_counters with next, conditioned on the row still holding prev
// (attribute absent when prev == nil). Ran inside the same transaction as the
// game-fund transfer, the optimistic condition serializes concurrent deposits:
// the loser's transaction cancels and surfaces as WalletBusy.
func (r *UserRepository) BumpDepositCounters(userID string, prev *wallet.GameDepositCounters, next wallet.GameDepositCounters) (types.TransactWriteItem, error) {
	nextAV, err := attributevalue.Marshal(next)
	if err != nil {
		return types.TransactWriteItem{}, err
	}
	values := map[string]types.AttributeValue{":next": nextAV, ":now": &types.AttributeValueMemberS{Value: NowStr()}}
	cond := "attribute_not_exists(#c)"
	if prev != nil {
		prevAV, err := attributevalue.Marshal(*prev)
		if err != nil {
			return types.TransactWriteItem{}, err
		}
		cond = "#c = :prev"
		values[":prev"] = prevAV
	}
	return r.users.BuildRawUpdateTxItem(userID, nil,
		"SET #c = :next, #u = :now", cond,
		map[string]string{"#c": "game_deposit_counters", "#u": "updated_at"}, values), nil
}

// AcceptGamblingAddendum stamps the current gambling addendum version and the
// acceptance timestamp. A separate document from the terms addendum: accepting
// one never implies the other. Partial update, for the same reason as above.
func (r *UserRepository) AcceptGamblingAddendum(ctx context.Context, userID string) error {
	now := NowStr()
	return r.users.UpsertAttrs(ctx, userID, nil, map[string]any{
		"gambling_addendum_version": wallet.CurrentGamblingAddendumVersion,
		"gambling_activated_at":     now,
		"updated_at":                now,
	})
}
