//go:build integration

package integration_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"gopkg.aoctech.app/wallet/api/internal/domain/id"
	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/pix"
	"gopkg.aoctech.app/wallet/api/internal/problem"
	"gopkg.aoctech.app/wallet/api/internal/repositories"
)

// fundReal credits the user's real wallet directly (a legacy deposit stand-in).
func fundReal(t *testing.T, h *harness, user string, amount int64) *wallet.Wallet {
	t.Helper()
	ctx := context.Background()
	real, err := h.repo.EnsureRealWallet(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.repo.Credit(ctx, repositories.Mutation{
		WalletID: real.WalletID, Amount: amount, EntryType: wallet.EntryDeposit,
		Ref: "seed", IdempotencyKey: "seed#" + id.New(), ReqHash: "seed",
	}); err != nil {
		t.Fatal(err)
	}
	return real
}

func TestDepositFullLoopAndReplay(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()

	dep, _, err := h.svc.InitiateDeposit(ctx, user, wallet.KYCVerified, 25000, "idem-1")
	if err != nil {
		t.Fatal(err)
	}
	dep2, _, err := h.svc.InitiateDeposit(ctx, user, wallet.KYCVerified, 25000, "idem-1")
	if err != nil {
		t.Fatal(err)
	}
	if dep.Txid != dep2.Txid {
		t.Fatalf("txid changed on replay: %s vs %s", dep.Txid, dep2.Txid)
	}
	if len(h.pix.CreatedCharges) != 1 {
		t.Fatalf("charges created = %d, want 1", len(h.pix.CreatedCharges))
	}

	h.pix.StageCharge(dep.Txid, 25000, pix.ChargeCompleted, cpf, "E2E-loop")
	for range 2 { // second call is the webhook retry
		if err := h.svc.ConfirmDeposit(ctx, dep.Txid, cpf, "Pagador", false); err != nil {
			t.Fatal(err)
		}
	}
	if got := balance(t, h, dep.WalletID); got != 25000 {
		t.Fatalf("balance = %d, want 25000", got)
	}
}

// Two paid charges that together exceed the R$1.000 daily cap: exactly one may
// credit, the other payer must be refunded.
func TestConfirmDepositConcurrentOverCapOnlyOneCredits(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()
	a := seedPendingDeposit(t, h, user, 60000)
	b := seedPendingDeposit(t, h, user, 60000)
	h.pix.StageCharge(a.Txid, 60000, pix.ChargeCompleted, cpf, "E2E-a")
	h.pix.StageCharge(b.Txid, 60000, pix.ChargeCompleted, cpf, "E2E-b")

	var wg sync.WaitGroup
	for _, tx := range []string{a.Txid, b.Txid} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = h.svc.ConfirmDeposit(ctx, tx, cpf, "P", false)
		}()
	}
	wg.Wait()
	// A loser that hit wallet-busy is settled by Inter's webhook retry.
	for _, tx := range []string{a.Txid, b.Txid} {
		_ = h.svc.ConfirmDeposit(ctx, tx, cpf, "P", false)
	}

	if got := balance(t, h, a.WalletID); got != 60000 {
		t.Fatalf("balance = %d, want exactly one 60000 credit", got)
	}
	if n := len(h.pix.Refunds); n != 1 {
		t.Fatalf("refunds = %d, want 1 (the over-cap payer)", n)
	}
}

func TestWithdrawalConcurrentSameDayOnlyOneSucceeds(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()
	real := fundReal(t, h, user, 500000)

	var ok atomic.Int32
	var wg sync.WaitGroup
	for i := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := h.svc.Withdraw(ctx, user, wallet.KYCVerified, 10000, fmt.Sprintf("k%d", i))
			var p *problem.Problem
			switch {
			case err == nil:
				ok.Add(1)
			case errors.As(err, &p) && (p.Type == problem.TypeDailyWithdrawLimit || p.Type == problem.TypeWalletBusy):
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 1 {
		t.Fatalf("successful withdrawals = %d, want 1", ok.Load())
	}
	if got := balance(t, h, real.WalletID); got != 490000 {
		t.Fatalf("balance = %d, want 490000", got)
	}
}

func TestWithdrawalReplayNoSecondPayoutNoSecondSlot(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()
	real := fundReal(t, h, user, 50000)

	w1, err := h.svc.Withdraw(ctx, user, wallet.KYCVerified, 10000, "same")
	if err != nil {
		t.Fatal(err)
	}
	w2, err := h.svc.Withdraw(ctx, user, wallet.KYCVerified, 10000, "same")
	if err != nil || w2.WithdrawalID != w1.WithdrawalID {
		t.Fatalf("replay: %+v %v", w2, err)
	}
	if len(h.pix.Transfers) != 1 {
		t.Fatalf("transfers = %d, want 1", len(h.pix.Transfers))
	}
	if got := balance(t, h, real.WalletID); got != 40000 {
		t.Fatalf("balance = %d, want 40000", got)
	}
}

func TestWithdrawalProcessingResolvedByReconcile(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()
	fundReal(t, h, user, 50000)

	h.pix.TransferErr = errors.New("bank timeout")
	w, err := h.svc.Withdraw(ctx, user, wallet.KYCVerified, 10000, "k1")
	if err != nil {
		t.Fatal(err)
	}
	if w.Status != wallet.WithdrawProcessing {
		t.Fatalf("status = %s, want processing", w.Status)
	}
	h.pix.StageTransferStatus(h.pix.Transfers[0].IdemKey, pix.TransferDone)
	if _, _, _, err := h.svc.ReconcileWithdrawals(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := h.repo.GetWithdrawal(ctx, w.WithdrawalID)
	if err != nil || got.Status != wallet.WithdrawCompleted {
		t.Fatalf("withdrawal = %+v err=%v", got, err)
	}
}

// Reviewer finding 2: a reversal replayed after its credit already committed
// must not release the daily slot a second time (it would erase a LATER
// withdrawal's slot and hand out an extra withdrawal).
func TestReversalReplayReleasesTheDailySlotOnlyOnce(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()
	fundReal(t, h, user, 100000)

	h.pix.TransferErr = errors.New("bank timeout")
	w1, err := h.svc.Withdraw(ctx, user, wallet.KYCVerified, 10000, "w1")
	if err != nil {
		t.Fatal(err)
	}
	h.pix.TransferErr = nil
	if err := h.svc.ReverseWithdrawal(ctx, w1.WithdrawalID); err != nil {
		t.Fatal(err)
	}
	// The slot came back, so the user withdraws again today.
	if _, err := h.svc.Withdraw(ctx, user, wallet.KYCVerified, 20000, "w2"); err != nil {
		t.Fatalf("slot not released: %v", err)
	}
	before := realCounters(t, h, user)
	if before.WithdrawCount != 1 || before.WithdrawSum != 20000 {
		t.Fatalf("counters after w2 = %+v", before)
	}

	// Replay of the reversal bookkeeping (crash after the credit, before the
	// status write): a decrement built from the CURRENT counters must be ignored.
	next := before
	next.WithdrawCount, next.WithdrawSum = 0, 0
	tx, err := h.userRepo.BumpRealDailyCounters(user, &before, next)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.repo.MarkWithdrawalReversed(ctx, w1.WithdrawalID, tx); err != nil {
		t.Fatal(err)
	}
	if after := realCounters(t, h, user); after != before {
		t.Fatalf("slot released twice: before %+v after %+v", before, after)
	}
}

// Reviewer finding 1: a stale/changed counter must never block the reversal
// bookkeeping; the money is already back, the slot is simply not released.
func TestStaleCounterNeverBlocksReversalBookkeeping(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()
	fundReal(t, h, user, 100000)
	h.pix.TransferErr = errors.New("bank timeout")
	w, err := h.svc.Withdraw(ctx, user, wallet.KYCVerified, 10000, "w1")
	if err != nil {
		t.Fatal(err)
	}
	stale := wallet.RealDailyCounters{DayKey: "1999-01-01", WithdrawCount: 7} // does not match the row
	tx, err := h.userRepo.BumpRealDailyCounters(user, &stale, wallet.RealDailyCounters{})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.repo.MarkWithdrawalReversed(ctx, w.WithdrawalID, tx); err != nil {
		t.Fatalf("a stale counter blocked the reversal: %v", err)
	}
	got, _ := h.repo.GetWithdrawal(ctx, w.WithdrawalID)
	if got.Status != wallet.WithdrawReversed {
		t.Fatalf("status = %s, want reversed", got.Status)
	}
}

func realCounters(t *testing.T, h *harness, user string) wallet.RealDailyCounters {
	t.Helper()
	u, err := h.userRepo.Get(context.Background(), user)
	if err != nil || u == nil || u.RealDailyCounters == nil {
		t.Fatalf("counters: %v %+v", err, u)
	}
	return *u.RealDailyCounters
}
