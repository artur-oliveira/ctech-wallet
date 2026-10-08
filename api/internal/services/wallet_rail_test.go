package services

import (
	"context"
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
