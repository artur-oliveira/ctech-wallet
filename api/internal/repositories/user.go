package repositories

import (
	"context"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"github.com/artur-oliveira/ctech-wallet/api/internal/config"
	"github.com/artur-oliveira/ctech-wallet/api/internal/domain/wallet"
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
	now := time.Now().UTC().Format(time.RFC3339)
	return r.users.UpsertAttrs(ctx, userID, nil, map[string]any{
		"terms_addendum_version": wallet.CurrentTermsAddendumVersion,
		"terms_accepted_at":      now,
		"updated_at":             now,
	})
}

// AcceptGamblingAddendum stamps the current gambling addendum version and the
// acceptance timestamp. A separate document from the terms addendum: accepting
// one never implies the other. Partial update, for the same reason as above.
func (r *UserRepository) AcceptGamblingAddendum(ctx context.Context, userID string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	return r.users.UpsertAttrs(ctx, userID, nil, map[string]any{
		"gambling_addendum_version": wallet.CurrentGamblingAddendumVersion,
		"gambling_activated_at":     now,
		"updated_at":                now,
	})
}
