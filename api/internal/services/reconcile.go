package services

import (
	"context"
	"log/slog"

	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/pix"
	"gopkg.aoctech.app/wallet/api/internal/repositories"
)

const reconcileBatch = 100

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
		res, qErr := s.pix.QueryTransfer(ctx, w.WithdrawalID)
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
			if s.reverse(ctx, w) {
				reversed++
			} else {
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
