//go:build integration

package integration_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/wallet/api/internal/domain/id"
	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/kycclient"
	"gopkg.aoctech.app/wallet/api/internal/pix"
	"gopkg.aoctech.app/wallet/api/internal/problem"
	"gopkg.aoctech.app/wallet/api/internal/repositories"
)

// setWalletFee simulates an admin editing the wallet's fee fields directly in
// DynamoDB (there is no API write path for fees).
func setWalletFee(t *testing.T, walletID string, bps, minFee, maxFee int64) {
	t.Helper()
	n := func(v int64) dtypes.AttributeValue { return &dtypes.AttributeValueMemberN{Value: fmt.Sprintf("%d", v)} }
	_, err := db.UpdateItem(context.Background(), &dynamodb.UpdateItemInput{
		TableName:                 aws.String(table(wallet.TableWallets)),
		Key:                       map[string]dtypes.AttributeValue{"pk": &dtypes.AttributeValueMemberS{Value: walletID}},
		UpdateExpression:          aws.String("SET fee_bps = :b, fee_min = :mn, fee_max = :mx"),
		ExpressionAttributeValues: map[string]dtypes.AttributeValue{":b": n(bps), ":mn": n(minFee), ":mx": n(maxFee)},
	})
	if err != nil {
		t.Fatalf("setWalletFee: %v", err)
	}
}

// setWalletDepositRange simulates an admin editing the wallet's deposit-range
// fields directly in DynamoDB (there is no API write path for them).
func setWalletDepositRange(t *testing.T, walletID string, minDep, maxDep int64) {
	t.Helper()
	n := func(v int64) dtypes.AttributeValue { return &dtypes.AttributeValueMemberN{Value: fmt.Sprintf("%d", v)} }
	_, err := db.UpdateItem(context.Background(), &dynamodb.UpdateItemInput{
		TableName:                 aws.String(table(wallet.TableWallets)),
		Key:                       map[string]dtypes.AttributeValue{"pk": &dtypes.AttributeValueMemberS{Value: walletID}},
		UpdateExpression:          aws.String("SET min_deposit = :mn, max_deposit = :mx"),
		ExpressionAttributeValues: map[string]dtypes.AttributeValue{":mn": n(minDep), ":mx": n(maxDep)},
	})
	if err != nil {
		t.Fatalf("setWalletDepositRange: %v", err)
	}
}

const cpf = "12345678901"

func verified() *kycclient.KYC { return &kycclient.KYC{Level: "verified", CPF: cpf} }

// activate opens the caller's game + sandbox wallets at the repository level,
// standing in for the consent-gated service flow (which TestActivateGambling*
// exercises directly). Tests that only need an activated user use this.
func activate(t *testing.T, h *harness, userID string) (game, sandbox *wallet.Wallet) {
	t.Helper()
	ctx := context.Background()
	// An activated user always has a real wallet — the ring-fence is funded from it.
	if _, err := h.repo.EnsureRealWallet(ctx, userID); err != nil {
		t.Fatalf("EnsureRealWallet: %v", err)
	}
	game, sandbox, err := h.repo.EnsureGamblingWallets(ctx, userID)
	if err != nil {
		t.Fatalf("EnsureGamblingWallets: %v", err)
	}
	return game, sandbox
}

// acceptGambling records the user's acceptance of the current gambling addendum,
// which service-level activation requires.
func acceptGambling(t *testing.T, h *harness, userID string) {
	t.Helper()
	if err := h.userRepo.AcceptGamblingAddendum(context.Background(), userID); err != nil {
		t.Fatalf("AcceptGamblingAddendum: %v", err)
	}
}

// fundedAndActivated is the common setup for ring-fence tests: a verified user
// with `amount` centavos in `real`, and game + sandbox wallets open (both empty).
func fundedAndActivated(t *testing.T, h *harness, amount int64) string {
	t.Helper()
	userID := "u-" + id.New()
	fund(t, h, userID, amount)
	activate(t, h, userID)
	return userID
}

// fund credits the user's real wallet directly (bypassing PIX) to set up balances.
func fund(t *testing.T, h *harness, userID string, amount int64) *wallet.Wallet {
	t.Helper()
	ctx := context.Background()
	real, err := h.repo.EnsureRealWallet(ctx, userID)
	if err != nil {
		t.Fatalf("EnsureRealWallet: %v", err)
	}
	if amount > 0 {
		if _, _, err := h.repo.Credit(ctx, repositories.Mutation{
			WalletID: real.WalletID, Amount: amount, EntryType: wallet.EntryDeposit,
			IdempotencyKey: "fund#" + id.New(), ReqHash: "fund",
		}); err != nil {
			t.Fatalf("fund credit: %v", err)
		}
	}
	real, _ = h.repo.GetWallet(ctx, real.WalletID)
	return real
}

func balance(t *testing.T, h *harness, walletID string) int64 {
	t.Helper()
	w, err := h.repo.GetWallet(context.Background(), walletID)
	if err != nil || w == nil {
		t.Fatalf("GetWallet(%s): %v", walletID, err)
	}
	return w.Balance
}

func wantProblem(t *testing.T, err error, typ string) {
	t.Helper()
	var p *problem.Problem
	if !errors.As(err, &p) {
		t.Fatalf("expected *problem.Problem, got %T: %v", err, err)
	}
	if p.Type != typ {
		t.Fatalf("problem type = %q, want %q", p.Type, typ)
	}
}

func TestDepositConfirmCreditsOnCPFMatch(t *testing.T) {
	ctx := context.Background()
	h := newHarness(&kycclient.KYC{Level: "verified", CPF: cpf})
	user := "u-" + id.New()

	dep, _, err := h.svc.InitiateDeposit(ctx, user, "verified", 5000)
	if err != nil {
		t.Fatalf("InitiateDeposit: %v", err)
	}
	h.pix.StageCharge(dep.Txid, 5000, pix.ChargeCompleted, cpf, "E2E-1")

	if err := h.svc.ConfirmDeposit(ctx, dep.Txid, cpf, "Fake Payer"); err != nil {
		t.Fatalf("ConfirmDeposit: %v", err)
	}
	if got := balance(t, h, dep.WalletID); got != 5000 {
		t.Fatalf("balance = %d, want 5000", got)
	}

	// Idempotent: re-confirming does not double-credit.
	_ = h.svc.ConfirmDeposit(ctx, dep.Txid, cpf, "Fake Payer")
	if got := balance(t, h, dep.WalletID); got != 5000 {
		t.Fatalf("balance after re-confirm = %d, want 5000", got)
	}
}

func TestEnsureRealWalletDoesNotCreateGamblingWallets(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()

	real, err := h.repo.EnsureRealWallet(ctx, user)
	if err != nil {
		t.Fatalf("EnsureRealWallet: %v", err)
	}
	if real == nil || real.Type != wallet.TypeReal {
		t.Fatalf("real wallet = %+v, want type %q", real, wallet.TypeReal)
	}

	// A user who has not activated has NO game and NO sandbox wallet.
	gotReal, game, sandbox, err := h.repo.LoadWallets(ctx, user)
	if err != nil {
		t.Fatalf("LoadWallets: %v", err)
	}
	if gotReal == nil || gotReal.WalletID != real.WalletID {
		t.Fatalf("LoadWallets real = %+v, want %s", gotReal, real.WalletID)
	}
	if game != nil || sandbox != nil {
		t.Fatalf("before activation game=%v sandbox=%v, want both nil", game, sandbox)
	}
}

func TestEnsureGamblingWalletsIsAtomicAndIdempotent(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()

	if _, err := h.repo.EnsureRealWallet(ctx, user); err != nil {
		t.Fatalf("EnsureRealWallet: %v", err)
	}

	game, sandbox, err := h.repo.EnsureGamblingWallets(ctx, user)
	if err != nil {
		t.Fatalf("EnsureGamblingWallets: %v", err)
	}
	if game.Type != wallet.TypeGame || sandbox.Type != wallet.TypeSandbox {
		t.Fatalf("types = %q/%q, want game/sandbox", game.Type, sandbox.Type)
	}
	if game.Balance != 0 || sandbox.Balance != 0 {
		t.Fatalf("new wallets must start at zero, got %d/%d", game.Balance, sandbox.Balance)
	}

	// Idempotent: a second call converges on the SAME wallets, never a second pair.
	game2, sandbox2, err := h.repo.EnsureGamblingWallets(ctx, user)
	if err != nil {
		t.Fatalf("EnsureGamblingWallets replay: %v", err)
	}
	if game2.WalletID != game.WalletID || sandbox2.WalletID != sandbox.WalletID {
		t.Fatalf("replay created new wallets: %s/%s vs %s/%s",
			game2.WalletID, sandbox2.WalletID, game.WalletID, sandbox.WalletID)
	}

	_, loadedGame, loadedSandbox, err := h.repo.LoadWallets(ctx, user)
	if err != nil {
		t.Fatalf("LoadWallets: %v", err)
	}
	if loadedGame == nil || loadedSandbox == nil {
		t.Fatal("after activation both game and sandbox must load")
	}
}

func TestDepositRejectsAmountOutsideGlobalRange(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()

	for _, amount := range []int64{wallet.DefaultMinDeposit - 1, wallet.DefaultMaxDeposit + 1} {
		dep, charge, err := h.svc.InitiateDeposit(ctx, user, "verified", amount)
		var p *problem.Problem
		if !errors.As(err, &p) || p.Type != problem.TypeDepositOutOfRange {
			t.Fatalf("InitiateDeposit(%d) err = %v, want deposit-out-of-range", amount, err)
		}
		// Nothing may be created for a rejected amount — no charge at the bank,
		// no pending deposit row.
		if dep != nil || charge != nil {
			t.Fatalf("InitiateDeposit(%d) returned dep=%v charge=%v, want both nil", amount, dep, charge)
		}
		if p.MinAmount != wallet.DefaultMinDeposit || p.MaxAmount != wallet.DefaultMaxDeposit {
			t.Errorf("problem bounds = [%d, %d], want [%d, %d]",
				p.MinAmount, p.MaxAmount, wallet.DefaultMinDeposit, wallet.DefaultMaxDeposit)
		}
	}

	// The boundaries themselves are accepted.
	if _, _, err := h.svc.InitiateDeposit(ctx, user, "verified", wallet.DefaultMinDeposit); err != nil {
		t.Fatalf("InitiateDeposit at min: %v", err)
	}
	if _, _, err := h.svc.InitiateDeposit(ctx, user, "verified", wallet.DefaultMaxDeposit); err != nil {
		t.Fatalf("InitiateDeposit at max: %v", err)
	}
}

func TestDepositUsesPerWalletRangeOverride(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()

	real, err := h.repo.EnsureRealWallet(ctx, user)
	if err != nil {
		t.Fatalf("EnsureRealWallet: %v", err)
	}
	setWalletDepositRange(t, real.WalletID, 5000, 20000)

	// Inside the global range but below this wallet's floor → rejected.
	if _, _, err := h.svc.InitiateDeposit(ctx, user, "verified", 4999); err == nil {
		t.Fatal("InitiateDeposit(4999) = nil, want deposit-out-of-range")
	}
	// Inside the global range but above this wallet's cap → rejected.
	if _, _, err := h.svc.InitiateDeposit(ctx, user, "verified", 20001); err == nil {
		t.Fatal("InitiateDeposit(20001) = nil, want deposit-out-of-range")
	}
	// Within the wallet's own range → accepted.
	if _, _, err := h.svc.InitiateDeposit(ctx, user, "verified", 20000); err != nil {
		t.Fatalf("InitiateDeposit(20000): %v", err)
	}
}

func TestDepositRejectsAndRefundsOnCPFMismatch(t *testing.T) {
	ctx := context.Background()
	h := newHarness(&kycclient.KYC{Level: "verified", CPF: cpf})
	user := "u-" + id.New()

	dep, _, err := h.svc.InitiateDeposit(ctx, user, "verified", 5000)
	if err != nil {
		t.Fatalf("InitiateDeposit: %v", err)
	}
	h.pix.StageCharge(dep.Txid, 5000, pix.ChargeCompleted, "99999999999", "E2E-9")

	if err := h.svc.ConfirmDeposit(ctx, dep.Txid, "99999999999", "Fake Payer"); err != nil {
		t.Fatalf("ConfirmDeposit: %v", err)
	}
	if got := balance(t, h, dep.WalletID); got != 0 {
		t.Fatalf("balance = %d, want 0 (rejected)", got)
	}
	if len(h.pix.Refunds) != 1 {
		t.Fatalf("expected one refund, got %d", len(h.pix.Refunds))
	}
}

func TestWithdrawHappyPath(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()
	real := fund(t, h, user, 20000)

	w, err := h.svc.Withdraw(ctx, user, "verified", 5000, "idem-"+id.New())
	if err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	if w.Status != wallet.WithdrawCompleted {
		t.Fatalf("status = %q, want completed", w.Status)
	}
	if w.PixKey != cpf {
		t.Fatalf("PixKey = %q, want the KYC CPF %q", w.PixKey, cpf)
	}
	fee := wallet.WithdrawalFee(5000, nil)
	want := int64(20000) - 5000 - fee
	if got := balance(t, h, real.WalletID); got != want {
		t.Fatalf("balance = %d, want %d", got, want)
	}
}

// TestWithdrawKeyNotFoundRefundsImmediately proves an unregistered PIX key
// (the CPF has no key at the bank) refunds the full amount+fee back to the
// wallet immediately, end-to-end against DynamoDB-local — it never leaves the
// withdrawal stuck in processing for the reconciliation job.
func TestWithdrawKeyNotFoundRefundsImmediately(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()
	real := fund(t, h, user, 20000)
	h.pix.TransferErr = pix.ErrKeyNotFound

	_, err := h.svc.Withdraw(ctx, user, "verified", 5000, "idem-"+id.New())
	wantProblem(t, err, problem.TypePixKeyNotFound)

	if got := balance(t, h, real.WalletID); got != 20000 {
		t.Fatalf("balance = %d, want 20000 (fully refunded)", got)
	}
}

func TestWithdrawUsesPerWalletFeeOverride(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()
	real := fund(t, h, user, 200000)

	// Admin sets a 1% fee with a higher cap directly on the wallet item (no API path).
	setWalletFee(t, real.WalletID, 100, 100, 5000)

	w, err := h.svc.Withdraw(ctx, user, "verified", 100000, "idem-"+id.New())
	if err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	if w.Fee != 1000 { // 1% of 100000, within [100,5000]
		t.Fatalf("fee = %d, want 1000 (per-wallet 1%%)", w.Fee)
	}
}

func TestWithdrawInsufficientBalance(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()
	fund(t, h, user, 100) // less than amount+fee

	_, err := h.svc.Withdraw(ctx, user, "verified", 5000, "idem-"+id.New())
	wantProblem(t, err, problem.TypeInsufficientBalance)
}

func TestWithdrawWalletBusy(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()
	real := fund(t, h, user, 20000)

	// Hold the wallet lock via the SAME locker the service uses → forces busy.
	release, ok, err := h.locker.Acquire(ctx, real.WalletID)
	if err != nil || !ok {
		t.Fatalf("setup lock: ok=%v err=%v", ok, err)
	}
	defer release()

	_, err = h.svc.Withdraw(ctx, user, "verified", 5000, "idem-"+id.New())
	wantProblem(t, err, problem.TypeWalletBusy)
}

// The purchase is atomic: game is debited and sandbox credited in one
// transaction, and `real` is not touched at all.
func TestSandboxPurchaseAtomic(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := fundedAndActivated(t, h, 10000)
	real, game, sandbox, err := h.repo.LoadWallets(ctx, user)
	if err != nil {
		t.Fatalf("LoadWallets: %v", err)
	}

	// Sandbox is bought from `game`, so the ring-fence must be funded first.
	if _, _, err := h.svc.FundGame(ctx, user, 5000, "idem-"+id.New()); err != nil {
		t.Fatalf("FundGame: %v", err)
	}
	if _, _, err := h.svc.PurchaseSandbox(ctx, user, 3000, "idem-"+id.New()); err != nil {
		t.Fatalf("PurchaseSandbox: %v", err)
	}

	if got := balance(t, h, real.WalletID); got != 5000 {
		t.Fatalf("real = %d, want 5000 (only the 5000 funded left it)", got)
	}
	if got := balance(t, h, game.WalletID); got != 2000 {
		t.Fatalf("game = %d, want 2000 (5000 funded - 3000 spent)", got)
	}
	if got := balance(t, h, sandbox.WalletID); got != 3000 {
		t.Fatalf("sandbox = %d, want 3000", got)
	}
}

func TestSandboxDebitNoNegative(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := fundedAndActivated(t, h, 0) // sandbox starts at 0

	_, err := h.svc.DebitSandbox(ctx, user, 500, "round-1", "bet")
	wantProblem(t, err, problem.TypeInsufficientBalance)
}

func TestIdempotentReplaySameResult(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()
	_, sandbox := activate(t, h, user)

	idem := "grant-1"
	if _, err := h.svc.CreditSandbox(ctx, user, 1000, idem, "bonus"); err != nil {
		t.Fatalf("credit 1: %v", err)
	}
	// Replay with the SAME key and payload → no double credit.
	if _, err := h.svc.CreditSandbox(ctx, user, 1000, idem, "bonus"); err != nil {
		t.Fatalf("credit replay: %v", err)
	}
	if got := balance(t, h, sandbox.WalletID); got != 1000 {
		t.Fatalf("sandbox = %d, want 1000 (replay must not double-credit)", got)
	}
}

func TestIdempotencyConflictOnDifferentPayload(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()
	activate(t, h, user)

	idem := "grant-2"
	if _, err := h.svc.CreditSandbox(ctx, user, 1000, idem, "bonus"); err != nil {
		t.Fatalf("credit: %v", err)
	}
	// Same key, different amount → conflict.
	_, err := h.svc.CreditSandbox(ctx, user, 2000, idem, "bonus")
	wantProblem(t, err, problem.TypeIdempotencyConflict)
}

// TestWithdrawConcurrentSameIdempotencyKeyExactlyOneTransfer proves F2's fix:
// N concurrent Withdraw calls with the SAME idempotency key must debit and
// PIX-transfer exactly once, never twice (the previous unconditional
// PutWithdrawal + a replay check taken before the lock let two racing calls
// both win).
func TestWithdrawConcurrentSameIdempotencyKeyExactlyOneTransfer(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()
	real := fund(t, h, user, 100000)

	const n = 10
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = h.svc.Withdraw(ctx, user, "verified", 5000, "idem-race")
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: unexpected error: %v", i, err)
		}
	}
	if got := len(h.pix.Transfers); got != 1 {
		t.Fatalf("expected exactly 1 PIX transfer call, got %d", got)
	}
	fee := wallet.WithdrawalFee(5000, nil)
	want := int64(100000) - 5000 - fee
	if got := balance(t, h, real.WalletID); got != want {
		t.Fatalf("balance = %d, want %d (double-debit if lower)", got, want)
	}
}

// TestRealDebitNeverTouchesSandboxWallet proves F3's new DebitReal only ever
// touches `real` — no cross-wallet side effect on sandbox/game.
func TestRealDebitNeverTouchesSandboxWallet(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()
	real := fund(t, h, user, 10000)

	entry, err := h.svc.DebitReal(ctx, user, 5000, "charge-1", "subscription")
	if err != nil {
		t.Fatal(err)
	}
	if entry.WalletID != real.WalletID {
		t.Fatalf("debited %q, want the real wallet %q", entry.WalletID, real.WalletID)
	}
	if got := balance(t, h, real.WalletID); got != 5000 {
		t.Fatalf("real balance = %d, want 5000", got)
	}
}

// TestListPendingDepositsOlderThanFindsAgedPending proves F6's sweep query:
// an aged pending deposit is returned, a fresh one is not.
func TestListPendingDepositsOlderThanFindsAgedPending(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	old := &wallet.PixDeposit{
		Txid: "old-" + id.New(), WalletID: "w1", UserID: "u1",
		AmountExpected: 1000, Status: wallet.DepositPending,
		CreatedAt: time.Now().Add(-4 * time.Minute).UTC().Format(time.RFC3339Nano),
		TTL:       time.Now().Add(1 * time.Minute).Unix(),
	}
	fresh := &wallet.PixDeposit{
		Txid: "fresh-" + id.New(), WalletID: "w1", UserID: "u1",
		AmountExpected: 1000, Status: wallet.DepositPending,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		TTL:       time.Now().Add(5 * time.Minute).Unix(),
	}
	if err := h.repo.PutDeposit(ctx, old); err != nil {
		t.Fatal(err)
	}
	if err := h.repo.PutDeposit(ctx, fresh); err != nil {
		t.Fatal(err)
	}

	found, err := h.repo.ListPendingDepositsOlderThan(ctx, time.Now().Add(-3*time.Minute), 50)
	if err != nil {
		t.Fatal(err)
	}
	var sawOld, sawFresh bool
	for _, d := range found {
		if d.Txid == old.Txid {
			sawOld = true
		}
		if d.Txid == fresh.Txid {
			sawFresh = true
		}
	}
	if !sawOld {
		t.Fatal("expected the aged pending deposit in the sweep list")
	}
	if sawFresh {
		t.Fatal("fresh pending deposit should not be swept yet")
	}
}

// TestSweepPendingDepositsCreditsOnceInterConfirms proves the sweep credits an
// aged pending deposit whose webhook never arrived, by reusing ConfirmDeposit.
func TestSweepPendingDepositsCreditsOnceInterConfirms(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()
	real, err := h.repo.EnsureRealWallet(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	txid := "sweep-" + id.New()
	dep := &wallet.PixDeposit{
		Txid: txid, WalletID: real.WalletID, UserID: user,
		AmountExpected: 5000, Status: wallet.DepositPending, PayerCPF: cpf,
		CreatedAt: time.Now().Add(-4 * time.Minute).UTC().Format(time.RFC3339Nano),
		TTL:       time.Now().Add(1 * time.Minute).Unix(),
	}
	if err := h.repo.PutDeposit(ctx, dep); err != nil {
		t.Fatal(err)
	}
	h.pix.StageCharge(txid, 5000, pix.ChargeCompleted, cpf, "E2E-sweep")

	swept, err := h.svc.SweepPendingDeposits(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if swept != 1 {
		t.Fatalf("swept = %d, want 1", swept)
	}
	if got := balance(t, h, real.WalletID); got != 5000 {
		t.Fatalf("balance = %d, want 5000 (deposit should have been credited)", got)
	}
}

// TestConcurrentCreditSameIdempotencyKeyAppliesOnce closes the remaining part
// of the audit's testing gap: N concurrent Credit calls with the SAME
// idempotency key must apply exactly once.
func TestConcurrentCreditSameIdempotencyKeyAppliesOnce(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()
	real, err := h.repo.EnsureRealWallet(ctx, user)
	if err != nil {
		t.Fatal(err)
	}

	const n = 10
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, errs[i] = h.repo.Credit(ctx, repositories.Mutation{
				WalletID: real.WalletID, Amount: 1000, EntryType: wallet.EntryDeposit,
				Ref: "concurrent-credit", IdempotencyKey: "credit-race#" + user, ReqHash: "same-hash",
			})
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: %v", i, err)
		}
	}
	if got := balance(t, h, real.WalletID); got != 1000 {
		t.Fatalf("balance = %d, want 1000 (double-credit if higher)", got)
	}
}

// TestConcurrentFundGameSameIdempotencyKeyAppliesOnce proves the real→game
// ring-fence transfer applies exactly once under concurrent identical calls.
func TestConcurrentFundGameSameIdempotencyKeyAppliesOnce(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := fundedAndActivated(t, h, 50000)
	real, err := h.repo.EnsureRealWallet(ctx, user)
	if err != nil {
		t.Fatal(err)
	}

	const n = 10
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, errs[i] = h.svc.FundGame(ctx, user, 5000, "fund-race")
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: %v", i, err)
		}
	}
	if got := balance(t, h, real.WalletID); got != 45000 {
		t.Fatalf("real balance = %d, want 45000 (double-fund if lower)", got)
	}
}

// TestConcurrentPurchaseSandboxSameIdempotencyKeyAppliesOnce proves the
// game→sandbox conversion applies exactly once under concurrent identical calls.
func TestConcurrentPurchaseSandboxSameIdempotencyKeyAppliesOnce(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()
	game, _ := activate(t, h, user)
	if _, _, err := h.repo.Credit(ctx, repositories.Mutation{
		WalletID: game.WalletID, Amount: 20000, EntryType: wallet.EntryGameFundCredit,
		Ref: "seed", IdempotencyKey: "seed-game#" + user, ReqHash: "seed",
	}); err != nil {
		t.Fatal(err)
	}

	const n = 10
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, errs[i] = h.svc.PurchaseSandbox(ctx, user, 5000, "purchase-race")
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: %v", i, err)
		}
	}
	if got := balance(t, h, game.WalletID); got != 15000 {
		t.Fatalf("game balance = %d, want 15000 (double-purchase if lower)", got)
	}
}
