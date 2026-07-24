package services

import (
	"context"
	"log/slog"
	"time"

	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/pix"
	"gopkg.aoctech.app/wallet/api/internal/repositories"
)

const reconcileBatch = 100

// sweepAgeThreshold: a pending PixDeposit gets one re-query once it's older than
// this. It must stay well below depositTTLMinutes so the re-query fallback runs
// while the row is still alive (SEC-02). With depositTTLMinutes=60 and this=10,
// the sweep has a 50m buffer before the row is TTL-deleted. The reconcile Lambda
// interval must be << this threshold (every 1–2 min) so a pending deposit is
// caught on its first or second sweep, never after it has already expired.
const sweepAgeThreshold = 10 * time.Minute

// staleHoldCeiling: no real cash-game session should run longer than this. A
// hold still `held` past the ceiling is Invariant #12's "money left in limbo"
// case, applied to holds.
const staleHoldCeiling = 24 * time.Hour

// ReconcileWithdrawals resolves withdrawals stuck in the processing state: it
// asks the bank whether each payout actually went through. Completed payouts are
// marked completed; payouts that never happened are reversed (the debited amount
// is credited back). A reversal whose credit-back fails is flagged refund_failed
// and raises an operational alarm — money is never left in limbo (spec §D/Risks).
func (s *WalletService) ReconcileWithdrawals(ctx context.Context) (resolved, reversed, alarmed int, err error) {
	ws, err := s.repo.ListProcessingWithdrawals(ctx, reconcileBatch)
	if err != nil {
		return 0, 0, 0, err
	}
	for i := range ws {
		w := ws[i]
		res, qErr := s.pix.QueryTransfer(ctx, interIdemKey(w.WithdrawalID))
		if qErr != nil {
			slog.Warn("reconcile: query transfer failed, will retry", "withdrawal_id", w.WithdrawalID, "err", qErr)
			continue
		}
		switch res.Status {
		case pix.TransferDone:
			if err := s.repo.UpdateWithdrawal(ctx, w.WithdrawalID, map[string]any{
				"status": wallet.WithdrawCompleted, "e2e_id": res.E2EID,
			}); err != nil {
				slog.Error("reconcile: mark completed failed", "withdrawal_id", w.WithdrawalID, "err", err)
				continue
			}
			s.broadcastWithdrawal(ctx, w.UserID, "withdraw_completed", w.WithdrawalID, w.Amount)
			resolved++
		case pix.TransferNotFound:
			// Acquire the per-wallet lock for the reversal so it is serialized
			// with every other real-wallet mutation (SEC-09). The synchronous
			// Withdraw path already holds this lock when it calls reverse; here
			// it is not held, so we take it explicitly.
			release, ok, lerr := s.lock.Acquire(ctx, w.WalletID)
			if lerr != nil || !ok {
				slog.Warn("reconcile: reverse lock unavailable, will retry", "withdrawal_id", w.WithdrawalID, "err", lerr)
				alarmed++
				break
			}
			if s.reverse(ctx, w) {
				release()
				reversed++
			} else {
				release()
				alarmed++
			}
		default:
			// Still pending at the bank — leave in processing for the next run.
		}
	}
	return resolved, reversed, alarmed, nil
}

// reverse credits the debited amount+fee back to the wallet, whether called
// synchronously (Withdraw: the PIX key turned out to be unregistered) or
// asynchronously (ReconcileWithdrawals: the payout never went through) — same
// idempotent reversal either way, so both notify the user identically.
func (s *WalletService) reverse(ctx context.Context, w wallet.Withdrawal) bool {
	total := w.Amount + w.Fee
	_, _, err := s.repo.Credit(ctx, repositories.Mutation{
		WalletID:       w.WalletID,
		Amount:         total,
		EntryType:      wallet.EntryReversal,
		Ref:            "reverse:" + w.WithdrawalID,
		IdempotencyKey: "reverse#" + w.WithdrawalID,
		ReqHash:        reqHash("reverse:"+w.WithdrawalID, total),
	})
	if err != nil {
		slog.Error("ALARM withdrawal reversal credit-back failed", "withdrawal_id", w.WithdrawalID, "amount", total, "err", err)
		_ = s.repo.UpdateWithdrawal(ctx, w.WithdrawalID, map[string]any{"status": wallet.WithdrawRefundFail})
		s.broadcastWithdrawal(ctx, w.UserID, "withdraw_refund_failed", w.WithdrawalID, w.Amount)
		return false
	}
	if err := s.repo.UpdateWithdrawal(ctx, w.WithdrawalID, map[string]any{"status": wallet.WithdrawReversed}); err != nil {
		slog.Error("reconcile: mark reversed failed", "withdrawal_id", w.WithdrawalID, "err", err)
	}
	s.broadcastWithdrawal(ctx, w.UserID, "withdraw_reversed", w.WithdrawalID, w.Amount)
	return true
}

// SweepPendingDeposits re-queries Inter once for every pending deposit
// approaching its TTL, reusing ConfirmDeposit's own idempotent credit logic —
// a webhook that never arrives (network issue, cold-start timeout, mTLS
// handshake failure) is the only case this changes: it used to silently
// expire, uncredited and unaccounted for.
func (s *WalletService) SweepPendingDeposits(ctx context.Context) (swept int, err error) {
	cutoff := time.Now().Add(-sweepAgeThreshold)
	deps, err := s.repo.ListPendingDepositsOlderThan(ctx, cutoff, reconcileBatch)
	if err != nil {
		return 0, err
	}
	for i := range deps {
		d := deps[i]
		if err := s.ConfirmDeposit(ctx, d.Txid, "", "", true); err != nil {
			slog.Warn("sweep: confirm-deposit failed, will retry next run", "txid", d.Txid, "err", err)
			continue
		}
		swept++
	}
	return swept, nil
}

// SweepStaleHolds raises an operational alarm for every hold stuck `held`
// past staleHoldCeiling. It never auto-releases or auto-cashes-out: the
// calling skill game's own crash-recovery may still resume the table and
// later call release/cashout itself, and auto-resolving here would race that
// and risk a double-credit — this is a page-a-human case, same as a stuck
// withdrawal (Invariant #12).
func (s *WalletService) SweepStaleHolds(ctx context.Context) (alarmed int, err error) {
	cutoff := time.Now().Add(-staleHoldCeiling)
	holds, err := s.repo.ScanStaleHolds(ctx, cutoff, reconcileBatch)
	if err != nil {
		return 0, err
	}
	for i := range holds {
		h := holds[i]
		slog.Error("ALARM stale game hold past ceiling", "hold_id", h.HoldID, "user_id", h.UserID,
			"wallet_id", h.WalletID, "amount", h.Amount, "table_ref", h.TableRef, "created_at", h.CreatedAt)
		alarmed++
	}
	return alarmed, nil
}
