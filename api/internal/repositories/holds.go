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
	_, err := r.holds.UpdateItemRaw(ctx, &dynamodb.UpdateItemInput{
		Key:                      map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: holdID}},
		UpdateExpression:         aws.String("SET #status = :to, updated_at = :now"),
		ConditionExpression:      aws.String("#status = :from"),
		ExpressionAttributeNames: map[string]string{"#status": "status"},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":to":   &types.AttributeValueMemberS{Value: toStatus},
			":from": &types.AttributeValueMemberS{Value: fromStatus},
			":now":  &types.AttributeValueMemberS{Value: NowStr()},
		},
	})
	if err != nil {
		if IsConditionFailed(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
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
