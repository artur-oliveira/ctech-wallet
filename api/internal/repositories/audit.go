package repositories

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/wallet/api/internal/config"
	"gopkg.aoctech.app/wallet/api/internal/domain/id"
	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
)

// AuditRepository is the append-only store for non-money events: consent,
// gambling activation, and every personal-limit change. It deliberately exposes
// no update and no delete — that absence is the guarantee.
type AuditRepository struct {
	audit Base
}

func NewAuditRepository(db *dynamodb.Client, cfg *config.Config) *AuditRepository {
	return &AuditRepository{audit: NewBase(db, cfg, wallet.TableAudit)}
}

// Append writes a new audit row, stamping EventID/CreatedAt when unset. The write
// is conditional on the row not already existing, so an existing event can never
// be overwritten — a replayed or forged write fails rather than mutating history.
func (r *AuditRepository) Append(ctx context.Context, e *wallet.AuditEvent) error {
	if e.EventID == "" {
		e.EventID = id.New()
	}
	if e.CreatedAt == "" {
		e.CreatedAt = NowStr()
	}
	e.SK = e.CreatedAt + "#" + e.EventID
	av, err := Encode(*e)
	if err != nil {
		return err
	}
	return r.audit.TransactWrite(ctx, []types.TransactWriteItem{r.audit.BuildPutTxItemIfAbsent(av)})
}

// List returns a user's audit trail, newest first.
func (r *AuditRepository) List(ctx context.Context, userID string, limit int) ([]wallet.AuditEvent, error) {
	res, err := r.audit.Query(ctx, QueryOpts{PK: userID, Limit: limit})
	if err != nil {
		return nil, err
	}
	out := make([]wallet.AuditEvent, 0, len(res.Items))
	for _, item := range res.Items {
		e, err := Decode[wallet.AuditEvent](item)
		if err != nil {
			return nil, err
		}
		out = append(out, *e)
	}
	return out, nil
}
