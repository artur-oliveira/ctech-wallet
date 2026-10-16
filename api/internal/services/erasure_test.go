package services

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"gopkg.aoctech.app/api-commons/erasure"
	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/kycclient"
	"gopkg.aoctech.app/wallet/api/internal/rail"
)

type fakeErasureRepo struct {
	deposits     []wallet.PixDeposit
	withdrawals  []wallet.Withdrawal
	holds        []wallet.Hold
	retained     map[string]any
	profileCalls int
	auditRows    int
	carried      map[string]wallet.SelfExclusion
	carriedUntil map[string]time.Time
}

func newFakeErasureRepo() *fakeErasureRepo {
	return &fakeErasureRepo{retained: map[string]any{}, carried: map[string]wallet.SelfExclusion{}, carriedUntil: map[string]time.Time{}}
}

func (f *fakeErasureRepo) DepositsForUser(context.Context, string) ([]wallet.PixDeposit, error) {
	return append([]wallet.PixDeposit(nil), f.deposits...), nil
}
func (f *fakeErasureRepo) WithdrawalsForUser(context.Context, string) ([]wallet.Withdrawal, error) {
	return append([]wallet.Withdrawal(nil), f.withdrawals...), nil
}
func (f *fakeErasureRepo) OpenHoldsForUser(context.Context, string) ([]wallet.Hold, error) {
	return f.holds, nil
}
func (f *fakeErasureRepo) Retain(_ context.Context, _, source, key string, item any, _ time.Time) error {
	if _, ok := f.retained[source+"#"+key]; !ok {
		f.retained[source+"#"+key] = item
	}
	return nil
}
func (f *fakeErasureRepo) StripDepositPII(_ context.Context, txid string) error {
	for i := range f.deposits {
		if f.deposits[i].Txid == txid {
			f.deposits[i].PayerCPF, f.deposits[i].PayerName = "", ""
		}
	}
	return nil
}
func (f *fakeErasureRepo) StripWithdrawalPII(_ context.Context, id string) error {
	for i := range f.withdrawals {
		if f.withdrawals[i].WithdrawalID == id {
			f.withdrawals[i].PixKey = ""
		}
	}
	return nil
}
func (f *fakeErasureRepo) EraseUserProfile(context.Context, string) (bool, error) {
	f.profileCalls++
	return true, nil
}
func (f *fakeErasureRepo) AnonymizeAudit(context.Context, string) (int, error) {
	n := f.auditRows
	f.auditRows = 0
	return n, nil
}
func (f *fakeErasureRepo) PutCarriedSelfExclusion(_ context.Context, h string, ex wallet.SelfExclusion, until time.Time) error {
	f.carried[h], f.carriedUntil[h] = ex, until
	return nil
}

var testNow = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

// stubCharges answers Inter's QueryCharge from a txid -> status map.
type stubCharges struct{ status map[string]string }

func (c *stubCharges) QueryDeposit(_ context.Context, txid string) (*rail.Charge, error) {
	return &rail.Charge{Txid: txid, Status: c.status[txid]}, nil
}

func newErasureSvc(repo *stubRepo, users *stubUserRepo, sp *stubSandboxPurchaseRepo, pp *stubProductPurchaseRepo, er *fakeErasureRepo, kyc *kycclient.KYC) *ErasureService {
	e := NewErasureService(repo, users, sp, pp, er, &stubKYC{rec: kyc}, "https://wallet.test")
	e.now = func() time.Time { return testNow }
	return e
}

func eraseMsg() erasure.Message {
	return erasure.Message{Version: erasure.Version, Type: erasure.TypeErase, RequestID: "req-1", Sub: "u1",
		Scope: erasure.ScopeAccount, Services: []string{wallet.ErasureServiceID}, IssuedAt: testNow}
}

func codes(bs []erasure.Blocker) []string {
	out := make([]string, 0, len(bs))
	for _, b := range bs {
		out = append(out, b.Code)
	}
	slices.Sort(out)
	return out
}

func TestBlockers_ZeroBalancesAndNothingPendingIsEligible(t *testing.T) {
	e := newErasureSvc(newStubRepo(), &stubUserRepo{}, newStubSandboxPurchaseRepo(), newStubProductPurchaseRepo(), newFakeErasureRepo(), &kycclient.KYC{})
	bs, err := e.Blockers(context.Background(), "u1")
	if err != nil || len(bs) != 0 {
		t.Fatalf("Blockers = %v, %v; want none", bs, err)
	}
}

func TestBlockers_ReportsEveryMoneyCondition(t *testing.T) {
	repo := newStubRepo()
	repo.real.Balance, repo.game.Balance, repo.sandbox.Balance = 1250, 300, 999 // sandbox never blocks (R12)
	er := newFakeErasureRepo()
	er.holds = []wallet.Hold{{HoldID: "h1", UserID: "u1", Status: wallet.HoldHeld}}
	er.deposits = []wallet.PixDeposit{{Txid: "t1", UserID: "u1", Status: wallet.DepositRefundFailed}}
	er.withdrawals = []wallet.Withdrawal{{WithdrawalID: "w1", UserID: "u1", Status: wallet.WithdrawProcessing}}
	sp := newStubSandboxPurchaseRepo()
	sp.purchases["s1"] = &wallet.SandboxPurchase{PurchaseID: "s1", UserID: "u1", Status: wallet.SandboxPurchasePending,
		CreatedAt: testNow.Add(-5 * time.Minute).Format(time.RFC3339Nano)}
	pp := newStubProductPurchaseRepo()
	// Unpaid and past its charge validity: can never be paid, must not block forever.
	pp.purchases["p1"] = &wallet.ProductPurchase{PurchaseID: "p1", UserID: "u1", Status: wallet.ProductPurchasePending,
		CreatedAt: testNow.Add(-3 * time.Hour).Format(time.RFC3339Nano)}
	e := newErasureSvc(repo, &stubUserRepo{}, sp, pp, er, &kycclient.KYC{})

	bs, err := e.Blockers(context.Background(), "u1")
	if err != nil {
		t.Fatalf("Blockers: %v", err)
	}
	want := []string{wallet.BlockerBalanceNonzero, wallet.BlockerBalanceNonzero, wallet.BlockerDepositPending,
		wallet.BlockerHoldOpen, wallet.BlockerPurchasePending, wallet.BlockerWithdrawalPending}
	slices.Sort(want)
	if got := codes(bs); !slices.Equal(got, want) {
		t.Fatalf("codes = %v, want %v", got, want)
	}
	for _, b := range bs {
		if b.Code != wallet.BlockerBalanceNonzero {
			continue
		}
		switch b.Detail["wallet"] {
		case wallet.TypeGame:
			if b.ActionURL != "https://wallet.test" || b.Detail["amount_cents"] != int64(300) {
				t.Fatalf("game blocker = %+v, want amount 300 and the wallet home link", b)
			}
		case wallet.TypeReal:
			if b.ActionURL != "https://wallet.test" || b.Detail["amount_cents"] != int64(1250) ||
				b.Detail["daily_withdraw_cap_cents"] != wallet.DefaultDailyWithdrawCap || b.Detail["days_needed"] != int64(1) {
				t.Fatalf("real blocker = %+v, want amount 1250, the wallet link and a one-day estimate", b)
			}
		}
	}

	pp.purchases["p2"] = &wallet.ProductPurchase{PurchaseID: "p2", UserID: "u1", Status: wallet.ProductPurchasePending,
		CreatedAt: testNow.Add(-time.Minute).Format(time.RFC3339Nano)}
	sp.purchases = map[string]*wallet.SandboxPurchase{}
	bs, _ = e.Blockers(context.Background(), "u1")
	if !slices.Contains(codes(bs), wallet.BlockerPurchasePending) {
		t.Fatal("a payable product charge must block")
	}
}

func TestWithdrawDaysNeeded(t *testing.T) {
	lim := wallet.DailyLimits{WithdrawCap: 100000, WithdrawCount: 1}
	for balance, want := range map[int64]int64{0: 0, 1: 1, 100000: 1, 100001: 2, 350000: 4} {
		if got := withdrawDaysNeeded(balance, lim); got != want {
			t.Errorf("withdrawDaysNeeded(%d) = %d, want %d", balance, got, want)
		}
	}
	if got := withdrawDaysNeeded(350000, wallet.DailyLimits{WithdrawCap: 100000, WithdrawCount: 2}); got != 2 {
		t.Errorf("two withdrawals a day: %d days, want 2", got)
	}
}

// R19: a paid charge with no payer identity stays `pending` (quarantined). It
// holds the user's money at Inter, so it must block; an abandoned QR must not.
func TestBlockers_PaidButUnconfirmedDepositBlocksAndAnAbandonedOneDoesNot(t *testing.T) {
	er := newFakeErasureRepo()
	old := testNow.Add(-48 * time.Hour).Format(time.RFC3339Nano)
	er.deposits = []wallet.PixDeposit{{Txid: "abandoned", UserID: "u1", Status: wallet.DepositPending, CreatedAt: old}}
	charges := &stubCharges{status: map[string]string{"abandoned": rail.ChargeActive}}
	e := newErasureSvc(newStubRepo(), &stubUserRepo{}, newStubSandboxPurchaseRepo(), newStubProductPurchaseRepo(), er, &kycclient.KYC{}).WithCharges(charges)
	if bs, err := e.Blockers(context.Background(), "u1"); err != nil || len(bs) != 0 {
		t.Fatalf("abandoned QR: %v, %v; want none", bs, err)
	}
	er.deposits = append(er.deposits, wallet.PixDeposit{Txid: "paid", UserID: "u1", Status: wallet.DepositPending, CreatedAt: old})
	charges.status["paid"] = rail.ChargeCompleted
	bs, err := e.Blockers(context.Background(), "u1")
	if err != nil || len(bs) != 1 || bs[0].Code != wallet.BlockerDepositQuarantined || bs[0].Detail["paid_unconfirmed"] != 1 {
		t.Fatalf("blockers = %+v, %v; want one %s", bs, err, wallet.BlockerDepositQuarantined)
	}
	// Without a charge client nothing old can be proven unpaid: fail safe.
	e = newErasureSvc(newStubRepo(), &stubUserRepo{}, newStubSandboxPurchaseRepo(), newStubProductPurchaseRepo(), er, &kycclient.KYC{})
	if bs, _ := e.Blockers(context.Background(), "u1"); len(bs) != 1 || bs[0].Detail["unchecked"] != 2 {
		t.Fatalf("no charge client: %+v; want unchecked 2", bs)
	}
}

func TestPurge_BlockerAcksBlockedAndTouchesNothing(t *testing.T) {
	repo := newStubRepo()
	repo.real.Balance = 5 // e.g. a PIX deposit that raced the lock
	er := newFakeErasureRepo()
	er.deposits = []wallet.PixDeposit{{Txid: "t1", UserID: "u1", Status: wallet.DepositConfirmed, PayerCPF: "52998224725"}}
	e := newErasureSvc(repo, &stubUserRepo{}, newStubSandboxPurchaseRepo(), newStubProductPurchaseRepo(), er, &kycclient.KYC{})

	ack, err := e.Purge(context.Background(), eraseMsg())
	if err != nil || ack.Result != erasure.ResultBlocked || len(ack.Blockers) == 0 {
		t.Fatalf("Purge = %+v, %v; want blocked with blockers", ack, err)
	}
	if len(er.retained) != 0 || er.profileCalls != 0 || er.deposits[0].PayerCPF == "" {
		t.Fatal("a blocked purge must not touch any data")
	}
}

func TestPurge_RetainsThenStripsAndIsIdempotent(t *testing.T) {
	er := newFakeErasureRepo()
	er.deposits = []wallet.PixDeposit{
		{Txid: "t1", UserID: "u1", Status: wallet.DepositConfirmed, PayerCPF: "52998224725", PayerName: "Fulano", AmountExpected: 100},
		{Txid: "t2", UserID: "u1", Status: wallet.DepositExpired}, // never paid: no PII, nothing to retain
	}
	er.withdrawals = []wallet.Withdrawal{{WithdrawalID: "w1", UserID: "u1", Status: wallet.WithdrawCompleted, PixKey: "52998224725"}}
	er.auditRows = 3
	e := newErasureSvc(newStubRepo(), &stubUserRepo{}, newStubSandboxPurchaseRepo(), newStubProductPurchaseRepo(), er, &kycclient.KYC{})

	ack, err := e.Purge(context.Background(), eraseMsg())
	if err != nil || ack.Result != erasure.ResultDone {
		t.Fatalf("Purge = %+v, %v", ack, err)
	}
	if ack.Counts[wallet.CountDepositsRetained] != 1 || ack.Counts[wallet.CountWithdrawalsRetained] != 1 ||
		ack.Counts[wallet.CountAuditAnonymized] != 3 || ack.Counts[wallet.CountUserProfileErased] != 1 {
		t.Fatalf("counts = %v", ack.Counts)
	}
	if d, ok := er.retained[wallet.TablePixDeposits+"#t1"].(wallet.PixDeposit); !ok || d.PayerCPF != "52998224725" {
		t.Fatalf("retained deposit = %#v, want the full copy", er.retained[wallet.TablePixDeposits+"#t1"])
	}
	if er.deposits[0].PayerCPF != "" || er.withdrawals[0].PixKey != "" {
		t.Fatal("operational rows still carry PII")
	}

	ack, err = e.Purge(context.Background(), eraseMsg())
	if err != nil || ack.Result != erasure.ResultDone || ack.Counts[wallet.CountDepositsRetained] != 0 {
		t.Fatalf("rerun = %+v, %v; want done with nothing left to retain", ack, err)
	}
	if d := er.retained[wallet.TablePixDeposits+"#t1"].(wallet.PixDeposit); d.PayerCPF != "52998224725" {
		t.Fatal("rerun overwrote the retained copy")
	}
}

func TestPurge_SelfExclusionCarriedByCPFHMAC(t *testing.T) {
	er := newFakeErasureRepo()
	users := &stubUserRepo{user: &wallet.User{SelfExclusion: &wallet.SelfExclusion{Period: "indefinite", RequestedAt: testNow.Format(time.RFC3339)}}}
	e := newErasureSvc(newStubRepo(), users, newStubSandboxPurchaseRepo(), newStubProductPurchaseRepo(), er,
		&kycclient.KYC{Level: "basic", CPF: "52998224725", CPFHMAC: "h1"})

	ack, err := e.Purge(context.Background(), eraseMsg())
	if err != nil || ack.Counts[wallet.CountSelfExclusionCarried] != 1 {
		t.Fatalf("Purge = %+v, %v", ack, err)
	}
	if er.carried["h1"].Period != "indefinite" || !er.carriedUntil["h1"].Equal(testNow.AddDate(wallet.ErasureRetentionYears, 0, 0)) {
		t.Fatalf("carried = %+v until %v", er.carried["h1"], er.carriedUntil["h1"])
	}
}

func TestPurge_SelfExcludedWithoutHMACIsRetriedNotDropped(t *testing.T) {
	er := newFakeErasureRepo()
	users := &stubUserRepo{user: &wallet.User{SelfExclusion: &wallet.SelfExclusion{Period: "indefinite", RequestedAt: testNow.Format(time.RFC3339)}}}
	e := newErasureSvc(newStubRepo(), users, newStubSandboxPurchaseRepo(), newStubProductPurchaseRepo(), er,
		&kycclient.KYC{Level: "basic", CPF: "52998224725"}) // account not upgraded yet

	if _, err := e.Purge(context.Background(), eraseMsg()); !errors.Is(err, errCPFHMACMissing) {
		t.Fatalf("err = %v, want errCPFHMACMissing (message redelivered)", err)
	}
	if er.profileCalls != 0 {
		t.Fatal("the profile (and its exclusion) must not be erased before the exclusion is carried")
	}
}

func TestPurge_SelfExcludedWithoutCPFCompletes(t *testing.T) {
	er := newFakeErasureRepo()
	users := &stubUserRepo{user: &wallet.User{SelfExclusion: &wallet.SelfExclusion{Period: "30d", Until: testNow.Add(time.Hour).Format(time.RFC3339)}}}
	e := newErasureSvc(newStubRepo(), users, newStubSandboxPurchaseRepo(), newStubProductPurchaseRepo(), er, &kycclient.KYC{})

	ack, err := e.Purge(context.Background(), eraseMsg())
	if err != nil || ack.Result != erasure.ResultDone || len(er.carried) != 0 {
		t.Fatalf("Purge = %+v, %v, carried %v; want done, nothing carried (no CPF to key on)", ack, err, er.carried)
	}
}
