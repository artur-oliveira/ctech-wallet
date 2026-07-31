package repositories

import (
	"context"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/problem"
)

const (
	holdStatusUpdateExpression    = "SET #status = :to, " + attributeUpdatedAt + " = :now"
	holdStatusConditionExpression = "#status = :from"
	holdStatusAttribute           = "status"
)

func holdStatusTransition(fromStatus, toStatus, timestamp string) (map[string]string, map[string]types.AttributeValue) {
	return map[string]string{"#status": holdStatusAttribute}, map[string]types.AttributeValue{
		":to":   &types.AttributeValueMemberS{Value: toStatus},
		":from": &types.AttributeValueMemberS{Value: fromStatus},
		":now":  &types.AttributeValueMemberS{Value: timestamp},
	}
}

// CreateHold debits amount from walletID and puts the Hold row in ONE
// TransactWriteItems alongside the ledger entry and idempotency guard — a
// crash between "money reserved" and "hold recorded" can never happen. holdID
// is caller-supplied and deterministic (see WalletService.HoldGame) so a
// replay can be re-fetched by id without the repository generating one.
//
// Idempotent: same idemKey → the prior Hold is returned (replayed=true).
// Insufficient balance is a normal *problem.Problem, same as any other debit.
func (r *WalletRepository) CreateHold(ctx context.Context, holdID, walletID, userID string, amount int64, tableRef, idemKey, reqHash string) (hold *wallet.Hold, replayed bool, err error) {
	prior, conflict, err := r.checkReplay(ctx, idemKey, reqHash)
	if err != nil {
		return nil, false, err
	}
	if conflict != nil {
		return nil, false, conflict
	}
	if prior != nil {
		h, err := r.GetHold(ctx, holdID)
		return h, true, err
	}

	w, err := r.GetWallet(ctx, walletID)
	if err != nil {
		return nil, false, err
	}
	if w == nil {
		return nil, false, problem.NotFound("carteira não encontrada")
	}

	now := NowStr()
	h := &wallet.Hold{
		HoldID:         holdID,
		WalletID:       walletID,
		UserID:         userID,
		Amount:         amount,
		TableRef:       tableRef,
		Status:         wallet.HoldHeld,
		IdempotencyKey: idemKey,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	hav, err := Encode(h)
	if err != nil {
		return nil, false, err
	}
	holdTx := r.holds.BuildPutTxItemIfAbsent(hav)

	entry := r.newEntry(walletID, wallet.EntryGameHoldDebit, -amount, w.Balance-amount, idemKey, tableRef)
	walletTx, err := r.balanceTx(walletID, amount, -1)
	if err != nil {
		return nil, false, err
	}
	ledgerTx, guardTx, err := r.ledgerAndGuardTx(entry, idemKey, reqHash)
	if err != nil {
		return nil, false, err
	}

	if err := r.wallets.TransactWrite(ctx, []types.TransactWriteItem{walletTx, ledgerTx, guardTx, holdTx}); err != nil {
		_, replayed, rErr := r.resolveTxErr(ctx, idemKey, reqHash, -1, err)
		if rErr != nil {
			return nil, false, rErr
		}
		if replayed {
			h2, gErr := r.GetHold(ctx, holdID)
			return h2, true, gErr
		}
		return nil, false, problem.InsufficientBalance()
	}
	return h, false, nil
}

// GetHold returns the hold, or nil if absent.
func (r *WalletRepository) GetHold(ctx context.Context, holdID string) (*wallet.Hold, error) {
	item, err := r.holds.GetItem(ctx, holdID)
	if err != nil || item == nil {
		return nil, err
	}
	return Decode[wallet.Hold](item)
}

// UpdateHoldStatus transitions a hold from fromStatus to toStatus, conditioned
// on the hold currently being in fromStatus — so a hold can only transition
// once; a second release/cashout racing the first fails closed instead of
// double-crediting. Returns false (no error) if the hold is not currently in
// fromStatus, which callers treat as a benign idempotent-replay case.
func (r *WalletRepository) UpdateHoldStatus(ctx context.Context, holdID, fromStatus, toStatus string) (bool, error) {
	names, values := holdStatusTransition(fromStatus, toStatus, NowStr())
	_, err := r.holds.UpdateItemRaw(ctx, &dynamodb.UpdateItemInput{
		Key:                       map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: holdID}},
		UpdateExpression:          aws.String(holdStatusUpdateExpression),
		ConditionExpression:       aws.String(holdStatusConditionExpression),
		ExpressionAttributeNames:  names,
		ExpressionAttributeValues: values,
	})
	if err != nil {
		if IsConditionFailed(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// ReleaseHoldAtomic credits the reserved amount and transitions held→released
// in the same transaction. A crash or retry can therefore never leave a
// credited hold in the held state and credit it again under another request key.
func (r *WalletRepository) ReleaseHoldAtomic(ctx context.Context, h *wallet.Hold, idemKey, reqHash string) (*wallet.Hold, bool, error) {
	prior, conflict, err := r.checkReplay(ctx, idemKey, reqHash)
	if err != nil {
		return nil, false, err
	}
	if conflict != nil {
		return nil, false, conflict
	}
	if prior != nil {
		resolved, err := r.GetHold(ctx, h.HoldID)
		return resolved, true, err
	}
	w, err := r.GetWallet(ctx, h.WalletID)
	if err != nil {
		return nil, false, err
	}
	entry := r.newEntry(h.WalletID, wallet.EntryGameHoldRelease, h.Amount, w.Balance+h.Amount, idemKey, h.TableRef)
	walletTx, err := r.balanceTx(h.WalletID, h.Amount, +1)
	if err != nil {
		return nil, false, err
	}
	ledgerTx, guardTx, err := r.ledgerAndGuardTx(entry, idemKey, reqHash)
	if err != nil {
		return nil, false, err
	}
	names, values := holdStatusTransition(wallet.HoldHeld, wallet.HoldReleased, NowStr())
	holdTx := r.holds.BuildRawUpdateTxItem(h.HoldID, nil,
		holdStatusUpdateExpression, holdStatusConditionExpression, names, values)
	if err := r.wallets.TransactWrite(ctx, []types.TransactWriteItem{walletTx, ledgerTx, guardTx, holdTx}); err != nil {
		if IsConditionFailed(err) {
			if prior, _, replayErr := r.checkReplay(ctx, idemKey, reqHash); replayErr == nil && prior != nil {
				resolved, getErr := r.GetHold(ctx, h.HoldID)
				return resolved, true, getErr
			}
			resolved, getErr := r.GetHold(ctx, h.HoldID)
			if getErr != nil {
				return nil, false, getErr
			}
			if resolved != nil && resolved.Status != wallet.HoldHeld {
				return resolved, true, nil
			}
		}
		return nil, false, err
	}
	h.Status = wallet.HoldReleased
	return h, false, nil
}

// CashoutHoldsAtomic credits a bounded final stack and consumes all referenced
// holds in one transaction. No partial status update can leave credited value
// backed by reusable holds.
func (r *WalletRepository) CashoutHoldsAtomic(ctx context.Context, walletID, userID string, amount int64, tableRef string, holds []*wallet.Hold, idemKey, reqHash string) (*wallet.LedgerEntry, bool, error) {
	prior, conflict, err := r.checkReplay(ctx, idemKey, reqHash)
	if err != nil {
		return nil, false, err
	}
	if conflict != nil {
		return nil, false, conflict
	}
	if prior != nil {
		return prior, true, nil
	}
	w, err := r.GetWallet(ctx, walletID)
	if err != nil {
		return nil, false, err
	}
	entry := r.newEntry(walletID, wallet.EntryGameCashoutCredit, amount, w.Balance+amount, idemKey, tableRef)
	walletTx, err := r.balanceTx(walletID, amount, +1)
	if err != nil {
		return nil, false, err
	}
	ledgerTx, guardTx, err := r.ledgerAndGuardTx(entry, idemKey, reqHash)
	if err != nil {
		return nil, false, err
	}
	items := []types.TransactWriteItem{walletTx, ledgerTx, guardTx}
	transitionTime := NowStr()
	for _, h := range holds {
		names, values := holdStatusTransition(wallet.HoldHeld, wallet.HoldSettled, transitionTime)
		values[":user"] = &types.AttributeValueMemberS{Value: userID}
		values[":table"] = &types.AttributeValueMemberS{Value: tableRef}
		items = append(items, r.holds.BuildRawUpdateTxItem(h.HoldID, nil,
			holdStatusUpdateExpression, holdStatusConditionExpression+" AND user_id = :user AND table_ref = :table",
			names, values))
	}
	if err := r.wallets.TransactWrite(ctx, items); err != nil {
		return r.resolveTxErr(ctx, idemKey, reqHash, +1, err)
	}
	return entry, false, nil
}

// ListOpenHoldsForWallet returns every currently-`held` hold on walletID, via
// gsi_hold_status filtered in memory (mirrors ScanStaleHolds' filter-after-GSI-
// query shape — no table scan). Used by the Invariant #13 conservation check
// (plan §6) to sum a user's open exposure alongside real.Balance+game.Balance.
func (r *WalletRepository) ListOpenHoldsForWallet(ctx context.Context, walletID string, limit int) ([]wallet.Hold, error) {
	res, err := r.holds.QueryGSI(ctx, wallet.GSIHoldStatus, "status", wallet.HoldHeld, limit, nil)
	if err != nil {
		return nil, err
	}
	out := make([]wallet.Hold, 0, len(res.Items))
	for _, it := range res.Items {
		h, err := Decode[wallet.Hold](it)
		if err != nil {
			return nil, err
		}
		if h.WalletID == walletID {
			out = append(out, *h)
		}
	}
	return out, nil
}

// ScanStaleHolds returns holds still `held` and created before cutoff, via
// gsi_hold_status — the stale-hold reconciliation sweep's work queue
// (Invariant #12 analog: a hold stuck open past the ceiling is money left in
// limbo, and only the wallet can independently notice it).
func (r *WalletRepository) ScanStaleHolds(ctx context.Context, cutoff time.Time, limit int) ([]wallet.Hold, error) {
	res, err := r.holds.QueryGSI(ctx, wallet.GSIHoldStatus, "status", wallet.HoldHeld, limit, nil)
	if err != nil {
		return nil, err
	}
	out := make([]wallet.Hold, 0, len(res.Items))
	for _, it := range res.Items {
		h, err := Decode[wallet.Hold](it)
		if err != nil {
			return nil, err
		}
		createdAt, err := time.Parse(time.RFC3339Nano, h.CreatedAt)
		if err != nil {
			continue // malformed timestamp — skip rather than fail the whole sweep
		}
		if createdAt.Before(cutoff) {
			out = append(out, *h)
		}
	}
	return out, nil
}
