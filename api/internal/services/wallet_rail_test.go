package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/kycclient"
	"gopkg.aoctech.app/wallet/api/internal/pix"
	"gopkg.aoctech.app/wallet/api/internal/problem"
)

var railKYC = &kycclient.KYC{Level: "enhanced", CPF: "12345678901"}

func newRailSvc(repo *stubRepo, users *stubUserRepo, pc pix.PixClient) *WalletService {
	return NewWalletService(repo, users, &stubAudit{}, &stubLocker{}, pc, &stubKYC{rec: railKYC})
}

func todayKey() string {
	day, _, _ := wallet.WindowKeys(time.Now())
	return day
}

func TestInitiateDepositRejectsOverDailyCapBeforeCharge(t *testing.T) {
	repo := newStubRepo()
	fake := pix.NewFake()
	users := &stubUserRepo{user: &wallet.User{RealDailyCounters: &wallet.RealDailyCounters{DayKey: todayKey(), DepositSum: 90000}}}
	svc := newRailSvc(repo, users, fake)

	_, _, err := svc.InitiateDeposit(context.Background(), "u1", wallet.KYCVerified, 20000, "k1")
	isProblem(t, err, problem.TypeDailyDepositLimit)
	if len(fake.CreatedCharges) != 0 {
		t.Fatal("a charge was opened for a rejected amount")
	}
	if repo.deposit != nil {
		t.Fatal("a pending deposit row was left behind by a rejected request")
	}
}

func TestInitiateDepositReplayReturnsSameChargeNoSecondCharge(t *testing.T) {
	repo := newStubRepo()
	fake := pix.NewFake()
	svc := newRailSvc(repo, &stubUserRepo{}, fake)

	d1, _, err := svc.InitiateDeposit(context.Background(), "u1", wallet.KYCVerified, 5000, "k1")
	if err != nil {
		t.Fatal(err)
	}
	d2, _, err := svc.InitiateDeposit(context.Background(), "u1", wallet.KYCVerified, 5000, "k1")
	if err != nil {
		t.Fatal(err)
	}
	if d1.Txid != d2.Txid {
		t.Fatalf("txid changed on replay: %s vs %s", d1.Txid, d2.Txid)
	}
	if len(fake.CreatedCharges) != 1 {
		t.Fatalf("charges created = %d, want 1", len(fake.CreatedCharges))
	}
}

func TestInitiateDepositReplayWithDifferentAmountConflicts(t *testing.T) {
	repo := newStubRepo()
	svc := newRailSvc(repo, &stubUserRepo{}, pix.NewFake())
	if _, _, err := svc.InitiateDeposit(context.Background(), "u1", wallet.KYCVerified, 5000, "k1"); err != nil {
		t.Fatal(err)
	}
	_, _, err := svc.InitiateDeposit(context.Background(), "u1", wallet.KYCVerified, 6000, "k1")
	isProblem(t, err, problem.TypeIdempotencyConflict)
}

func TestInitiateDepositRequiresKYC(t *testing.T) {
	svc := newRailSvc(newStubRepo(), &stubUserRepo{}, pix.NewFake())
	if _, _, err := svc.InitiateDeposit(context.Background(), "u1", "", 5000, "k1"); err == nil {
		t.Fatal("empty KYC level must be rejected")
	}
}

func TestInitiateDepositAmountOutsideRange(t *testing.T) {
	fake := pix.NewFake()
	svc := newRailSvc(newStubRepo(), &stubUserRepo{}, fake)
	_, _, err := svc.InitiateDeposit(context.Background(), "u1", wallet.KYCVerified, 50, "k1")
	isProblem(t, err, problem.TypeDepositOutOfRange)
	if len(fake.CreatedCharges) != 0 {
		t.Fatal("charge opened for out-of-range amount")
	}
}

func paidDepositFixture(amount int64) (*stubRepo, *pix.FakePixClient) {
	repo := newStubRepo()
	repo.deposit = &wallet.PixDeposit{Txid: "tx1", WalletID: "w-real", UserID: "u1", AmountExpected: amount, Status: wallet.DepositPending}
	fake := pix.NewFake()
	fake.StageCharge("tx1", amount, pix.ChargeCompleted, "", "E2E-1")
	return repo, fake
}

func TestConfirmDepositOverDailyCapIsRefundedNotCredited(t *testing.T) {
	repo, fake := paidDepositFixture(20000)
	users := &stubUserRepo{user: &wallet.User{RealDailyCounters: &wallet.RealDailyCounters{DayKey: todayKey(), DepositSum: 90000}}}
	svc := newRailSvc(repo, users, fake)

	if err := svc.ConfirmDeposit(context.Background(), "tx1", "***456789**", "Pagador", false); err != nil {
		t.Fatalf("ConfirmDeposit: %v", err)
	}
	if len(repo.creditCalls) != 0 {
		t.Fatalf("over-cap deposit was credited: %+v", repo.creditCalls)
	}
	if len(fake.Refunds) != 1 {
		t.Fatalf("refunds = %d, want 1 (payer must get the money back)", len(fake.Refunds))
	}
	if len(users.realCountersBumped) != 0 {
		t.Fatal("counter bumped for a refunded deposit")
	}
}

func TestConfirmDepositWithinCapCreditsAndBumpsCounter(t *testing.T) {
	repo, fake := paidDepositFixture(30000)
	users := &stubUserRepo{}
	svc := newRailSvc(repo, users, fake)

	if err := svc.ConfirmDeposit(context.Background(), "tx1", "***456789**", "Pagador", false); err != nil {
		t.Fatalf("ConfirmDeposit: %v", err)
	}
	if len(repo.creditCalls) != 1 || repo.creditCalls[0].Amount != 30000 {
		t.Fatalf("credits = %+v", repo.creditCalls)
	}
	if len(users.realCountersBumped) != 1 || users.realCountersBumped[0].DepositSum != 30000 || users.realCountersBumped[0].DayKey != todayKey() {
		t.Fatalf("counter bumps = %+v", users.realCountersBumped)
	}
}

func TestConfirmDepositPaidWithoutPayerCPFStaysPendingAndCreditsNothing(t *testing.T) {
	repo, fake := paidDepositFixture(10000)
	svc := newRailSvc(repo, &stubUserRepo{}, fake)

	if err := svc.ConfirmDeposit(context.Background(), "tx1", "", "", true); err == nil {
		t.Fatal("a paid deposit with no payer identity must be quarantined with an error")
	}
	if len(repo.creditCalls) != 0 {
		t.Fatal("credited without payer identity")
	}
	if repo.deposit.Status != wallet.DepositPending {
		t.Fatalf("deposit left pending state: %s", repo.deposit.Status)
	}
}

func withdrawFixture(balance int64) (*stubRepo, *stubUserRepo, *pix.FakePixClient, *WalletService) {
	repo := newStubRepo()
	repo.real.Balance = balance
	users := &stubUserRepo{}
	fake := pix.NewFake()
	return repo, users, fake, newRailSvc(repo, users, fake)
}

func TestWithdrawDebitsExactAmountNoFeeToKYCCPF(t *testing.T) {
	repo, _, fake, svc := withdrawFixture(50000)
	w, err := svc.Withdraw(context.Background(), "u1", wallet.KYCVerified, 30000, "k1")
	if err != nil {
		t.Fatal(err)
	}
	if w.Status != wallet.WithdrawCompleted {
		t.Fatalf("status %s", w.Status)
	}
	if len(repo.debitCalls) != 1 || repo.debitCalls[0].Amount != 30000 || repo.debitCalls[0].EntryType != wallet.EntryWithdraw {
		t.Fatalf("debits = %+v", repo.debitCalls)
	}
	if len(fake.Transfers) != 1 || fake.Transfers[0].PixKey != railKYC.CPF {
		t.Fatalf("payout did not go to the KYC CPF: %+v", fake.Transfers)
	}
}

func TestWithdrawSecondOfTheDayIsRejected(t *testing.T) {
	_, _, fake, svc := withdrawFixture(50000)
	if _, err := svc.Withdraw(context.Background(), "u1", wallet.KYCVerified, 10000, "k1"); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Withdraw(context.Background(), "u1", wallet.KYCVerified, 10000, "k2")
	isProblem(t, err, problem.TypeDailyWithdrawLimit)
	if len(fake.Transfers) != 1 {
		t.Fatalf("transfers = %d, want 1", len(fake.Transfers))
	}
}

func TestWithdrawOverDailyAmountCap(t *testing.T) {
	_, _, fake, svc := withdrawFixture(500000)
	_, err := svc.Withdraw(context.Background(), "u1", wallet.KYCVerified, 100001, "k1")
	isProblem(t, err, problem.TypeDailyWithdrawLimit)
	if len(fake.Transfers) != 0 {
		t.Fatal("payout sent for a rejected withdrawal")
	}
}

func TestWithdrawReplaySameKeyNoSecondPayout(t *testing.T) {
	repo, _, fake, svc := withdrawFixture(50000)
	a, err := svc.Withdraw(context.Background(), "u1", wallet.KYCVerified, 10000, "k1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.Withdraw(context.Background(), "u1", wallet.KYCVerified, 10000, "k1")
	if err != nil || a.WithdrawalID != b.WithdrawalID {
		t.Fatalf("replay: %+v %v", b, err)
	}
	if len(fake.Transfers) != 1 || len(repo.debitCalls) != 1 {
		t.Fatalf("transfers=%d debits=%d, want 1 each", len(fake.Transfers), len(repo.debitCalls))
	}
}

func TestWithdrawKeyNotFoundReverseGivesTheSlotBack(t *testing.T) {
	repo, users, fake, svc := withdrawFixture(50000)
	fake.TransferErr = pix.ErrKeyNotFound
	_, err := svc.Withdraw(context.Background(), "u1", wallet.KYCVerified, 10000, "k1")
	isProblem(t, err, problem.TypePixKeyNotFound)
	if len(repo.creditCalls) != 1 || repo.creditCalls[0].EntryType != wallet.EntryReversal {
		t.Fatalf("not refunded: %+v", repo.creditCalls)
	}
	if got := users.user.RealDailyCounters; got == nil || got.WithdrawCount != 0 || got.WithdrawSum != 0 {
		t.Fatalf("daily slot not released: %+v", got)
	}
	fake.TransferErr = nil
	if _, err := svc.Withdraw(context.Background(), "u1", wallet.KYCVerified, 10000, "k2"); err != nil {
		t.Fatalf("slot was not released after the reversal: %v", err)
	}
}

func TestWithdrawBelowMinimumUnlessFullBalance(t *testing.T) {
	_, _, _, svc := withdrawFixture(5000)
	if _, err := svc.Withdraw(context.Background(), "u1", wallet.KYCVerified, 50, "k1"); err == nil {
		t.Fatal("below minimum must be rejected")
	}
	_, _, _, svc2 := withdrawFixture(50)
	if _, err := svc2.Withdraw(context.Background(), "u1", wallet.KYCVerified, 50, "k2"); err != nil {
		t.Fatalf("emptying the whole balance below the minimum must pass: %v", err)
	}
}

func TestWithdrawRequiresEnhancedKYC(t *testing.T) {
	_, _, _, svc := withdrawFixture(50000)
	if _, err := svc.Withdraw(context.Background(), "u1", "basic", 10000, "k1"); err == nil {
		t.Fatal("basic KYC must be rejected")
	}
}

func TestWithdrawTransferFailureLeavesProcessing(t *testing.T) {
	repo, _, fake, svc := withdrawFixture(50000)
	fake.TransferErr = errors.New("bank timeout")
	w, err := svc.Withdraw(context.Background(), "u1", wallet.KYCVerified, 10000, "k1")
	if err != nil || w.Status != wallet.WithdrawProcessing {
		t.Fatalf("w=%+v err=%v", w, err)
	}
	if len(repo.creditCalls) != 0 {
		t.Fatal("must not reverse on an ambiguous bank failure; reconcile resolves it")
	}
}

// Reviewer finding 3: the same Idempotency-Key with a different amount is a
// conflict, never a silent replay of the first withdrawal.
func TestWithdrawSameKeyDifferentAmountConflicts(t *testing.T) {
	_, _, fake, svc := withdrawFixture(50000)
	if _, err := svc.Withdraw(context.Background(), "u1", wallet.KYCVerified, 10000, "k1"); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Withdraw(context.Background(), "u1", wallet.KYCVerified, 20000, "k1")
	isProblem(t, err, problem.TypeIdempotencyConflict)
	if len(fake.Transfers) != 1 {
		t.Fatalf("transfers = %d, want 1", len(fake.Transfers))
	}
}

// Reviewer finding 4: no CPF on the KYC record means no payout key.
func TestWithdrawWithoutKYCCPFIsRejectedBeforeDebit(t *testing.T) {
	repo := newStubRepo()
	repo.real.Balance = 50000
	fake := pix.NewFake()
	svc := NewWalletService(repo, &stubUserRepo{}, &stubAudit{}, &stubLocker{}, fake, &stubKYC{rec: &kycclient.KYC{Level: "enhanced"}})
	_, err := svc.Withdraw(context.Background(), "u1", wallet.KYCVerified, 10000, "k1")
	isProblem(t, err, problem.TypeKYCNotVerified)
	if len(repo.debitCalls) != 0 || len(fake.Transfers) != 0 {
		t.Fatal("money moved without a destination CPF")
	}
}
