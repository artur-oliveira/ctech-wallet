package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/kycclient"
	"gopkg.aoctech.app/wallet/api/internal/rail"
)

// scriptedRail is a Rail whose answers a test sets, and which records what it
// was asked. It asks for the KYC identity and has a grace, like BillingRail.
type scriptedRail struct {
	charge    *rail.Charge
	queryErr  error
	openErr   error
	opened    []rail.DepositRequest
	refunds   []string // idempotency keys
	payout    *rail.TransferResult
	payoutErr error
	payouts   []string
	status    *rail.TransferResult
	statusErr error
	grace     time.Duration
}

func (r *scriptedRail) NeedsIdentity() bool        { return true }
func (r *scriptedRail) PayoutGrace() time.Duration { return r.grace }

func (r *scriptedRail) OpenDeposit(_ context.Context, in rail.DepositRequest) (*rail.Charge, error) {
	r.opened = append(r.opened, in)
	if r.openErr != nil {
		return nil, r.openErr
	}
	return &rail.Charge{Txid: in.Txid, Amount: in.AmountCents, Status: rail.ChargeActive, QRCode: "qr"}, nil
}

func (r *scriptedRail) QueryDeposit(_ context.Context, txid string) (*rail.Charge, error) {
	if r.queryErr != nil {
		return nil, r.queryErr
	}
	if r.charge == nil {
		return nil, errors.New("not found")
	}
	return r.charge, nil
}

func (r *scriptedRail) RefundDeposit(_ context.Context, _, _ string, _ int64, idemKey string) (*rail.TransferResult, error) {
	r.refunds = append(r.refunds, idemKey)
	return &rail.TransferResult{Status: rail.TransferDone}, nil
}

func (r *scriptedRail) Payout(_ context.Context, _ string, _ int64, idemKey string) (*rail.TransferResult, error) {
	r.payouts = append(r.payouts, idemKey)
	return r.payout, r.payoutErr
}

func (r *scriptedRail) QueryPayout(context.Context, string) (*rail.TransferResult, error) {
	return r.status, r.statusErr
}

type countingKYC struct {
	rec   *kycclient.KYC
	calls int
}

func (k *countingKYC) Get(context.Context, string) (*kycclient.KYC, error) {
	k.calls++
	return k.rec, nil
}

func billingSvc(repo Repo, r rail.Rail, kyc KYCClient) *WalletService {
	return NewWalletService(repo, &stubUserRepo{}, &stubAudit{}, &stubLocker{}, r, kyc)
}

func paidCharge(payer string) *rail.Charge {
	return &rail.Charge{Txid: "tx1", Amount: 10000, Status: rail.ChargeCompleted, E2EID: "in_1", PayerCPF: payer,
		Payments: []rail.Payment{{E2EID: "in_1", Amount: 10000, PayerCPF: payer}}}
}

// ── deposits ────────────────────────────────────────────────────────────────

func TestADepositThroughBillingCarriesTheKYCIdentityAndNoOtherRailIsAsked(t *testing.T) {
	k := &countingKYC{rec: &kycclient.KYC{Level: "enhanced", CPF: "12345678901", LegalName: "Fulano de Tal", Email: "f@x.com"}}
	r := &scriptedRail{}
	svc := billingSvc(newStubRepo(), r, k)
	if _, _, err := svc.InitiateDeposit(context.Background(), "u1", wallet.KYCVerified, 5000, "k1"); err != nil {
		t.Fatal(err)
	}
	if len(r.opened) != 1 || r.opened[0].TaxID != "12345678901" || r.opened[0].Name != "Fulano de Tal" || r.opened[0].Email != "f@x.com" || r.opened[0].UserID != "u1" {
		t.Fatalf("opened %+v", r.opened)
	}
}

func TestTheInterPathMakesNoKYCCallWhenOpeningADeposit(t *testing.T) {
	k := &countingKYC{rec: railKYC}
	svc := NewWalletService(newStubRepo(), &stubUserRepo{}, &stubAudit{}, &stubLocker{}, rail.NewFake(), k)
	if _, _, err := svc.InitiateDeposit(context.Background(), "u1", wallet.KYCVerified, 5000, "k1"); err != nil {
		t.Fatal(err)
	}
	if k.calls != 0 {
		t.Fatalf("the Inter path read the KYC %d times to open a deposit", k.calls)
	}
}

func TestBillingDownWhileDepositingLeavesARetryThatWorks(t *testing.T) {
	repo := newStubRepo()
	r := &scriptedRail{openErr: errors.New("billing 503")}
	svc := billingSvc(repo, r, &countingKYC{rec: railKYC})
	if _, _, err := svc.InitiateDeposit(context.Background(), "u1", wallet.KYCVerified, 5000, "k1"); err == nil {
		t.Fatal("the deposit call must fail closed while Billing is down")
	}
	// Billing recovers; the same request goes through on the reserved deposit.
	r.openErr = nil
	dep, charge, err := svc.InitiateDeposit(context.Background(), "u1", wallet.KYCVerified, 5000, "k1")
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if dep == nil || charge == nil || charge.QRCode != "qr" {
		t.Fatalf("retry returned %+v %+v", dep, charge)
	}
}

func TestAPaidDepositWithTheRightPayerIsCreditedOnce(t *testing.T) {
	repo := newStubRepo()
	repo.deposit = &wallet.PixDeposit{Txid: "tx1", WalletID: "w-real", UserID: "u1", AmountExpected: 10000, Status: wallet.DepositPending}
	r := &scriptedRail{charge: paidCharge("12345678901")}
	svc := billingSvc(repo, r, &countingKYC{rec: railKYC})
	if err := svc.ConfirmDeposit(context.Background(), "tx1", "", "", false); err != nil {
		t.Fatal(err)
	}
	if len(repo.creditCalls) != 1 || repo.creditCalls[0].Amount != 10000 {
		t.Fatalf("credits %+v", repo.creditCalls)
	}
	if len(r.refunds) != 0 {
		t.Fatal("a good deposit is never refunded")
	}
}

func TestAPaidDepositBillingCallsAMismatchIsRefundedAndNeverCredited(t *testing.T) {
	repo := newStubRepo()
	repo.deposit = &wallet.PixDeposit{Txid: "tx1", WalletID: "w-real", UserID: "u1", AmountExpected: 10000, Status: wallet.DepositPending}
	r := &scriptedRail{charge: paidCharge(rail.MismatchPayer)}
	svc := billingSvc(repo, r, &countingKYC{rec: railKYC})
	if err := svc.ConfirmDeposit(context.Background(), "tx1", "", "", false); err != nil {
		t.Fatal(err)
	}
	if len(repo.creditCalls) != 0 {
		t.Fatal("credited a deposit Billing said was paid by someone else")
	}
	if len(r.refunds) != 1 || r.refunds[0] != "refund#tx1" {
		t.Fatalf("refunds %v", r.refunds)
	}
}

func TestAPaidDepositWhoseCPFTheWalletDoesNotMatchIsRefundedEvenIfBillingSaidMatch(t *testing.T) {
	repo := newStubRepo()
	repo.deposit = &wallet.PixDeposit{Txid: "tx1", WalletID: "w-real", UserID: "u1", AmountExpected: 10000, Status: wallet.DepositPending}
	r := &scriptedRail{charge: paidCharge("99999999999")} // Billing said match; the KYC CPF is 12345678901
	svc := billingSvc(repo, r, &countingKYC{rec: railKYC})
	if err := svc.ConfirmDeposit(context.Background(), "tx1", "", "", false); err != nil {
		t.Fatal(err)
	}
	if len(repo.creditCalls) != 0 || len(r.refunds) != 1 {
		t.Fatalf("credits %d refunds %d: both checks have to pass", len(repo.creditCalls), len(r.refunds))
	}
}

func TestAPaidDepositWithNoPayerYetIsNeitherCreditedNorRefunded(t *testing.T) {
	repo := newStubRepo()
	repo.deposit = &wallet.PixDeposit{Txid: "tx1", WalletID: "w-real", UserID: "u1", AmountExpected: 10000, Status: wallet.DepositPending}
	r := &scriptedRail{charge: paidCharge("")}
	svc := billingSvc(repo, r, &countingKYC{rec: railKYC})
	if err := svc.ConfirmDeposit(context.Background(), "tx1", "", "", true); err == nil {
		t.Fatal("a paid deposit with no payer yet must be held with an error the sweep keeps retrying")
	}
	if len(repo.creditCalls) != 0 || len(r.refunds) != 0 || repo.deposit.Status != wallet.DepositPending {
		t.Fatalf("credits %d refunds %d status %s", len(repo.creditCalls), len(r.refunds), repo.deposit.Status)
	}
}

func TestAPaidForLessThanExpectedIsRefundedNeverCredited(t *testing.T) {
	repo := newStubRepo()
	repo.deposit = &wallet.PixDeposit{Txid: "tx1", WalletID: "w-real", UserID: "u1", AmountExpected: 10000, Status: wallet.DepositPending}
	c := paidCharge("12345678901")
	c.Amount, c.Payments[0].Amount = 4000, 4000
	r := &scriptedRail{charge: c}
	svc := billingSvc(repo, r, &countingKYC{rec: railKYC})
	if err := svc.ConfirmDeposit(context.Background(), "tx1", "", "", false); err != nil {
		t.Fatal(err)
	}
	if len(repo.creditCalls) != 0 || len(r.refunds) != 1 {
		t.Fatalf("credits %d refunds %d", len(repo.creditCalls), len(r.refunds))
	}
}

func TestTwoWakeUpsForOneDepositCreditOnce(t *testing.T) {
	repo := newStubRepo()
	repo.deposit = &wallet.PixDeposit{Txid: "tx1", WalletID: "w-real", UserID: "u1", AmountExpected: 10000, Status: wallet.DepositPending}
	r := &scriptedRail{charge: paidCharge("12345678901")}
	svc := billingSvc(repo, r, &countingKYC{rec: railKYC})
	for i := 0; i < 2; i++ {
		if err := svc.ConfirmDeposit(context.Background(), "tx1", "", "", false); err != nil {
			t.Fatal(err)
		}
	}
	if len(repo.creditCalls) != 1 {
		t.Fatalf("credited %d times", len(repo.creditCalls))
	}
}

// ── withdrawals ─────────────────────────────────────────────────────────────

func withdrawWith(r *scriptedRail) (*stubRepo, *WalletService) {
	repo := newStubRepo()
	repo.real.Balance = 50000
	return repo, billingSvc(repo, r, &countingKYC{rec: railKYC})
}

func TestAPayoutBillingHasOnlyAcceptedStaysProcessing(t *testing.T) {
	repo, svc := withdrawWith(&scriptedRail{payout: &rail.TransferResult{Status: rail.TransferProcessing}})
	w, err := svc.Withdraw(context.Background(), "u1", wallet.KYCVerified, 30000, "k1")
	if err != nil {
		t.Fatal(err)
	}
	if w.Status != wallet.WithdrawProcessing {
		t.Fatalf("status %s: only the bank's word completes a withdrawal", w.Status)
	}
	if len(repo.debitCalls) != 1 {
		t.Fatalf("debits %d", len(repo.debitCalls))
	}
}

func TestAPayoutThatTimesOutStaysProcessingAndIsNeverReversedByTheCall(t *testing.T) {
	repo, svc := withdrawWith(&scriptedRail{payoutErr: context.DeadlineExceeded})
	w, err := svc.Withdraw(context.Background(), "u1", wallet.KYCVerified, 30000, "k1")
	if err != nil {
		t.Fatal(err)
	}
	if w.Status != wallet.WithdrawProcessing || len(repo.creditCalls) != 0 {
		t.Fatalf("status %s credits %d: a timeout is not a failure", w.Status, len(repo.creditCalls))
	}
}

func reconcileWith(t *testing.T, r *scriptedRail, age time.Duration) (*reconRepo, int, int, int) {
	t.Helper()
	repo := &reconRepo{stubRepo: newStubRepo(), processing: []wallet.Withdrawal{
		{WithdrawalID: "wd1", WalletID: "w-real", Amount: 5000, Status: wallet.WithdrawProcessing, CreatedAt: time.Now().Add(-age).UTC().Format(time.RFC3339Nano)},
	}}
	repo.withdrawals["wd1"] = &repo.processing[0]
	svc := NewWalletService(repo, &stubUserRepo{}, &stubAudit{}, &stubLocker{}, r, &stubKYC{rec: &kycclient.KYC{}})
	resolved, reversed, alarmed, err := svc.ReconcileWithdrawals(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return repo, resolved, reversed, alarmed
}

func TestReconcileLeavesAWithdrawalAloneWhenTheLookupFails(t *testing.T) {
	repo, resolved, reversed, alarmed := reconcileWith(t, &scriptedRail{statusErr: errors.New("connection reset"), grace: 10 * time.Minute}, time.Hour)
	if resolved+reversed+alarmed != 0 || repo.credited != 0 || repo.withdrawals["wd1"].Status != wallet.WithdrawProcessing {
		t.Fatalf("a failed lookup changed something: %d %d %d credited %d", resolved, reversed, alarmed, repo.credited)
	}
}

func TestReconcileDoesNotBelieveUnknownAboutAFreshWithdrawalOnABillingRail(t *testing.T) {
	r := &scriptedRail{status: &rail.TransferResult{Status: rail.TransferNotFound}, grace: 10 * time.Minute}
	repo, _, reversed, _ := reconcileWith(t, r, time.Minute)
	if reversed != 0 || repo.credited != 0 {
		t.Fatal("reversed a one-minute-old withdrawal Billing might simply not show yet")
	}
	repo, _, reversed, _ = reconcileWith(t, r, time.Hour)
	if reversed != 1 || repo.credited != 1 {
		t.Fatalf("an hour-old unknown payout is reversed once: reversed %d credited %d", reversed, repo.credited)
	}
}

func TestReconcileCompletesAPayoutBillingSettled(t *testing.T) {
	r := &scriptedRail{status: &rail.TransferResult{Status: rail.TransferDone, E2EID: "E2E9"}, grace: 10 * time.Minute}
	repo, resolved, _, _ := reconcileWith(t, r, time.Minute)
	if resolved != 1 || repo.withdrawals["wd1"].Status != wallet.WithdrawCompleted {
		t.Fatalf("resolved %d status %s", resolved, repo.withdrawals["wd1"].Status)
	}
}

func TestReconcileKeepsWaitingOnAPayoutStillProcessing(t *testing.T) {
	r := &scriptedRail{status: &rail.TransferResult{Status: rail.TransferProcessing}, grace: 10 * time.Minute}
	repo, resolved, reversed, alarmed := reconcileWith(t, r, time.Hour)
	if resolved+reversed+alarmed != 0 || repo.withdrawals["wd1"].Status != wallet.WithdrawProcessing {
		t.Fatalf("%d %d %d status %s", resolved, reversed, alarmed, repo.withdrawals["wd1"].Status)
	}
}

// ── partial payers from the webhook ─────────────────────────────────────────

func namedKYC() *countingKYC {
	return &countingKYC{rec: &kycclient.KYC{Level: "enhanced", CPF: "12345678901", LegalName: "Artur Oliveira Carvalho"}}
}

func partialCharge(cpf, name string) *rail.Charge {
	c := paidCharge(cpf)
	c.PayerName, c.Payments[0].PayerName = name, name
	return c
}

func pendingDeposit() *stubRepo {
	repo := newStubRepo()
	repo.deposit = &wallet.PixDeposit{Txid: "tx1", WalletID: "w-real", UserID: "u1", AmountExpected: 10000, Status: wallet.DepositPending}
	return repo
}

func TestAMaskedCPFThatAgreesWithTheKYCAndANameThatMatchesIsCreditedAtOnce(t *testing.T) {
	repo := pendingDeposit()
	r := &scriptedRail{charge: partialCharge("***456789**", "ARTUR OLIVEIRA CARVALHO")}
	if err := billingSvc(repo, r, namedKYC()).ConfirmDeposit(context.Background(), "tx1", "", "", false); err != nil {
		t.Fatal(err)
	}
	if len(repo.creditCalls) != 1 || len(r.refunds) != 0 {
		t.Fatalf("credits %d refunds %d", len(repo.creditCalls), len(r.refunds))
	}
}

func TestAMaskedCPFWithADigitThatDiffersIsRefundedWhateverTheName(t *testing.T) {
	repo := pendingDeposit()
	r := &scriptedRail{charge: partialCharge("***456780**", "Artur Oliveira Carvalho")}
	if err := billingSvc(repo, r, namedKYC()).ConfirmDeposit(context.Background(), "tx1", "", "", false); err != nil {
		t.Fatal(err)
	}
	if len(repo.creditCalls) != 0 || len(r.refunds) != 1 {
		t.Fatalf("credits %d refunds %d", len(repo.creditCalls), len(r.refunds))
	}
}

func TestADifferentPayerNameIsRefundedEvenWhenTheMaskedDigitsAgree(t *testing.T) {
	repo := pendingDeposit()
	r := &scriptedRail{charge: partialCharge("***456789**", "Beltrano de Souza")}
	if err := billingSvc(repo, r, namedKYC()).ConfirmDeposit(context.Background(), "tx1", "", "", false); err != nil {
		t.Fatal(err)
	}
	if len(repo.creditCalls) != 0 || len(r.refunds) != 1 || r.refunds[0] != "refund#tx1" {
		t.Fatalf("credits %d refunds %v", len(repo.creditCalls), r.refunds)
	}
}

func TestTheInterPathNamesNoOneAndIsNotHeldToAName(t *testing.T) {
	repo := pendingDeposit()
	r := &scriptedRail{charge: paidCharge("12345678901")} // no PayerName, as on the Inter path
	if err := billingSvc(repo, r, namedKYC()).ConfirmDeposit(context.Background(), "tx1", "", "", false); err != nil {
		t.Fatal(err)
	}
	if len(repo.creditCalls) != 1 {
		t.Fatalf("credits %d", len(repo.creditCalls))
	}
}

func TestAPayoutTheRailRefusedAtOnceReturnsTheMoneyAndIsNeverCompleted(t *testing.T) {
	repo, svc := withdrawWith(&scriptedRail{payout: &rail.TransferResult{Status: rail.TransferFailed}})
	if _, err := svc.Withdraw(context.Background(), "u1", wallet.KYCVerified, 30000, "k1"); err == nil {
		t.Fatal("a refused withdrawal is an error to the caller")
	}
	if len(repo.creditCalls) != 1 {
		t.Fatalf("the debit goes back once: %d", len(repo.creditCalls))
	}
}

func TestReconcileReversesAFailedPayoutAtOnceButWaitsOnAnUnknownOne(t *testing.T) {
	failed := &scriptedRail{status: &rail.TransferResult{Status: rail.TransferFailed}, grace: 10 * time.Minute}
	repo, _, reversed, _ := reconcileWith(t, failed, time.Second)
	if reversed != 1 || repo.credited != 1 {
		t.Fatalf("a rail's own 'failed' needs no grace: reversed %d credited %d", reversed, repo.credited)
	}
	unknown := &scriptedRail{status: &rail.TransferResult{Status: rail.TransferNotFound}, grace: 10 * time.Minute}
	repo, _, reversed, _ = reconcileWith(t, unknown, time.Second)
	if reversed != 0 || repo.credited != 0 {
		t.Fatal("an unknown payout still waits for its grace")
	}
}
