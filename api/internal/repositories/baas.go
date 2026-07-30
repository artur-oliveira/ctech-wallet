package repositories

import (
	"context"
	"errors"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/wallet/api/internal/config"
	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
)

// BaasRepository owns persistence for Asaas custody: the per-user lifecycle
// record (wallet_baas_accounts) and the transfer-authorization lookup table
// (wallet_transfer_intents). See
// docs/plans/2026-07-30-asaas-baas-implementation-plan.md §2.3, §2.4.
type BaasRepository struct {
	accounts Base
	intents  Base
	med      Base
}

func NewBaasRepository(db *dynamodb.Client, cfg *config.Config) *BaasRepository {
	return &BaasRepository{
		accounts: NewBase(db, cfg, wallet.TableBaasAccounts),
		intents:  NewBase(db, cfg, wallet.TableTransferIntents),
		med:      NewBase(db, cfg, wallet.TableMedReceivables),
	}
}

// --- wallet_baas_accounts ---

func (r *BaasRepository) GetBaasAccount(ctx context.Context, userID string) (*wallet.BaasAccount, error) {
	item, err := r.accounts.GetItem(ctx, userID)
	if err != nil || item == nil {
		return nil, err
	}
	return Decode[wallet.BaasAccount](item)
}

func (r *BaasRepository) GetBaasAccountByProviderID(ctx context.Context, providerAccountID string) (*wallet.BaasAccount, error) {
	res, err := r.accounts.QueryGSI(ctx, wallet.GSIBaasAccountID, "provider_account_id", providerAccountID, 1, nil)
	if err != nil {
		return nil, err
	}
	if len(res.Items) == 0 {
		return nil, nil
	}
	return Decode[wallet.BaasAccount](res.Items[0])
}

func (r *BaasRepository) PutBaasAccount(ctx context.Context, a *wallet.BaasAccount) error {
	av, err := Encode(a)
	if err != nil {
		return err
	}
	return r.accounts.PutItem(ctx, av)
}

func (r *BaasRepository) UpdateBaasAccount(ctx context.Context, userID string, updates map[string]any) error {
	updates["updated_at"] = NowStr()
	_, err := r.accounts.UpdateItem(ctx, userID, nil, updates)
	return err
}

// ListBaasAccountsByStatus returns accounts in the given status via the
// status GSI — the conservation-check sweep's work queue (plan §6): it only
// ever needs to walk `approved` accounts, since a non-approved user has no
// real wallet yet (nothing to conserve).
func (r *BaasRepository) ListBaasAccountsByStatus(ctx context.Context, status string, limit int) ([]wallet.BaasAccount, error) {
	res, err := r.accounts.QueryGSI(ctx, wallet.GSIBaasStatus, "status", status, limit, nil)
	if err != nil {
		return nil, err
	}
	out := make([]wallet.BaasAccount, 0, len(res.Items))
	for _, it := range res.Items {
		a, err := Decode[wallet.BaasAccount](it)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, nil
}

// --- wallet_transfer_intents ---

// ErrTransferIntentExists means PutTransferIntentIfAbsent lost a race (or is
// a genuine replay) — a row for this ExternalReference already exists. The
// caller should treat this as benign: the reference itself already encodes
// idempotency (e.g. "sbxg#"+idemKey), so a duplicate submission attempt must
// never be retried as a fresh CreateTransfer.
var ErrTransferIntentExists = errors.New("repositories: transfer intent already exists")

// PutTransferIntentIfAbsent writes a new intent row before its CreateTransfer
// call is ever sent (plan §2.3 step 1) — attribute_not_exists on the
// ExternalReference partition key, so the same reference is never overwritten.
func (r *BaasRepository) PutTransferIntentIfAbsent(ctx context.Context, t *wallet.TransferIntent) error {
	av, err := Encode(t)
	if err != nil {
		return err
	}
	item := r.intents.BuildPutTxItemIfAbsent(av)
	if err := r.intents.TransactWrite(ctx, []types.TransactWriteItem{item}); err != nil {
		if IsConditionFailed(err) {
			return ErrTransferIntentExists
		}
		return err
	}
	return nil
}

func (r *BaasRepository) GetTransferIntent(ctx context.Context, externalReference string) (*wallet.TransferIntent, error) {
	item, err := r.intents.GetItem(ctx, externalReference)
	if err != nil || item == nil {
		return nil, err
	}
	return Decode[wallet.TransferIntent](item)
}

func (r *BaasRepository) UpdateTransferIntent(ctx context.Context, externalReference string, updates map[string]any) error {
	updates["updated_at"] = NowStr()
	_, err := r.intents.UpdateItem(ctx, externalReference, nil, updates)
	return err
}

// GetTransferIntentByRef finds an intent by its Ref field (e.g. a sandbox
// purchase's ledger credit SK — plan §9.1a's reversal lookup). No table scan:
// Ref is not indexed, so this is intentionally left as a documented gap for
// callers that already know the ExternalReference directly; §9.1a derives it
// deterministically ("sbxg#"+idemKey) rather than searching by Ref.

// ListTransferIntentsByStatus returns intents in the given status via the
// status GSI — the reconcile job's work queue for stuck legs (plan §6, §9.1a).
func (r *BaasRepository) ListTransferIntentsByStatus(ctx context.Context, status string, limit int) ([]wallet.TransferIntent, error) {
	res, err := r.intents.QueryGSI(ctx, wallet.GSIIntentStatus, "status", status, limit, nil)
	if err != nil {
		return nil, err
	}
	out := make([]wallet.TransferIntent, 0, len(res.Items))
	for _, it := range res.Items {
		t, err := Decode[wallet.TransferIntent](it)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, nil
}

// --- wallet_med_receivables (plan §7.3) ---

// PutMedReceivableIfAbsent writes a new receivable row, keyed by a
// deterministic ReceivableID derived from the MED event's own reference
// ("med-recv#"+ref) — a redelivered webhook always computes the same ID, so
// this is naturally idempotent: a second attempt's condition fails and is a
// benign no-op, never a duplicate debt record.
func (r *BaasRepository) PutMedReceivableIfAbsent(ctx context.Context, m *wallet.MedReceivable) error {
	av, err := Encode(m)
	if err != nil {
		return err
	}
	item := r.med.BuildPutTxItemIfAbsent(av)
	if err := r.med.TransactWrite(ctx, []types.TransactWriteItem{item}); err != nil {
		if IsConditionFailed(err) {
			return nil
		}
		return err
	}
	return nil
}

func (r *BaasRepository) UpdateMedReceivable(ctx context.Context, receivableID string, updates map[string]any) error {
	updates["updated_at"] = NowStr()
	_, err := r.med.UpdateItem(ctx, receivableID, nil, updates)
	return err
}

// ListOpenMedReceivablesForWallet returns every open receivable on walletID
// via the status GSI, filtered in memory (same shape as
// ListOpenHoldsForWallet) — the gate FundGame/InitiateDeposit/Withdraw check
// before acting on a wallet with an outstanding clawback debt.
func (r *BaasRepository) ListOpenMedReceivablesForWallet(ctx context.Context, walletID string, limit int) ([]wallet.MedReceivable, error) {
	res, err := r.med.QueryGSI(ctx, wallet.GSIMedStatus, "status", wallet.MedReceivableOpen, limit, nil)
	if err != nil {
		return nil, err
	}
	out := make([]wallet.MedReceivable, 0, len(res.Items))
	for _, it := range res.Items {
		m, err := Decode[wallet.MedReceivable](it)
		if err != nil {
			return nil, err
		}
		if m.WalletID == walletID {
			out = append(out, *m)
		}
	}
	return out, nil
}
