package services

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/artur-oliveira/ctech-wallet/api/internal/domain/wallet"
	"github.com/artur-oliveira/ctech-wallet/api/internal/kycclient"
	"github.com/artur-oliveira/ctech-wallet/api/internal/pix"
	"github.com/artur-oliveira/ctech-wallet/api/internal/problem"
	"github.com/artur-oliveira/ctech-wallet/api/internal/repositories"
)

// --- stubs ---

type stubRepo struct {
	real, game, sandbox wallet.Wallet
	notActivated        bool // no game/sandbox wallets — user never opted in
	deposit             *wallet.PixDeposit
	withdrawals         map[string]*wallet.Withdrawal
	depositStatus       string
	depositE2E          string
	depositPayerCPF     string
	depositPayerName    string
	creditCalls         []repositories.Mutation
	debitCalls          []repositories.Mutation
	debitErr            error
	debitFeeErr         error
	debitFeeCalled      bool
	transferErr         error
	transferCalled      bool
}

func newStubRepo() *stubRepo {
	return &stubRepo{
		real:        wallet.Wallet{WalletID: "w-real", UserID: "u1", Type: wallet.TypeReal},
		game:        wallet.Wallet{WalletID: "w-game", UserID: "u1", Type: wallet.TypeGame},
		sandbox:     wallet.Wallet{WalletID: "w-sand", UserID: "u1", Type: wallet.TypeSandbox},
		withdrawals: map[string]*wallet.Withdrawal{},
	}
}

func (s *stubRepo) GetWallet(_ context.Context, id string) (*wallet.Wallet, error) {
	switch id {
	case s.real.WalletID:
		return &s.real, nil
	case s.game.WalletID:
		return &s.game, nil
	case s.sandbox.WalletID:
		return &s.sandbox, nil
	}
	return nil, nil
}
func (s *stubRepo) EnsureRealWallet(_ context.Context, _ string) (*wallet.Wallet, error) {
	return &s.real, nil
}
func (s *stubRepo) EnsureGamblingWallets(_ context.Context, _ string) (*wallet.Wallet, *wallet.Wallet, error) {
	s.notActivated = false
	return &s.game, &s.sandbox, nil
}
func (s *stubRepo) LoadWallets(_ context.Context, _ string) (*wallet.Wallet, *wallet.Wallet, *wallet.Wallet, error) {
	if s.notActivated {
		return &s.real, nil, nil, nil
	}
	return &s.real, &s.game, &s.sandbox, nil
}
func (s *stubRepo) Credit(_ context.Context, m repositories.Mutation) (*wallet.LedgerEntry, bool, error) {
	s.creditCalls = append(s.creditCalls, m)
	return &wallet.LedgerEntry{WalletID: m.WalletID, Amount: m.Amount, Type: m.EntryType}, false, nil
}
func (s *stubRepo) Debit(_ context.Context, m repositories.Mutation) (*wallet.LedgerEntry, bool, error) {
	s.debitCalls = append(s.debitCalls, m)
	if s.debitErr != nil {
		return nil, false, s.debitErr
	}
	return &wallet.LedgerEntry{WalletID: m.WalletID, Amount: -m.Amount, Type: m.EntryType}, false, nil
}
func (s *stubRepo) DebitWithFee(_ context.Context, walletID string, amount, fee int64, _, _, _ string) (*wallet.LedgerEntry, *wallet.LedgerEntry, bool, error) {
	s.debitFeeCalled = true
	if s.debitFeeErr != nil {
		return nil, nil, false, s.debitFeeErr
	}
	return &wallet.LedgerEntry{WalletID: walletID, Amount: -amount}, &wallet.LedgerEntry{WalletID: walletID, Amount: -fee}, false, nil
}
func (s *stubRepo) Transfer(_ context.Context, from, to string, amount int64, dt, ct, _, _, _ string) (*wallet.LedgerEntry, *wallet.LedgerEntry, bool, error) {
	s.transferCalled = true
	if s.transferErr != nil {
		return nil, nil, false, s.transferErr
	}
	return &wallet.LedgerEntry{WalletID: from, Amount: -amount, Type: dt}, &wallet.LedgerEntry{WalletID: to, Amount: amount, Type: ct}, false, nil
}
func (s *stubRepo) Statement(_ context.Context, _ string, _ int, _ map[string]types.AttributeValue) (*repositories.QueryResult, error) {
	return &repositories.QueryResult{}, nil
}
func (s *stubRepo) PutDeposit(_ context.Context, d *wallet.PixDeposit) error {
	s.deposit = d
	return nil
}
func (s *stubRepo) GetDeposit(_ context.Context, _ string) (*wallet.PixDeposit, error) {
	return s.deposit, nil
}
func (s *stubRepo) UpdateDepositStatus(_ context.Context, _, status, e2e string) error {
	s.depositStatus = status
	s.depositE2E = e2e
	return nil
}
func (s *stubRepo) UpdateDepositPayer(_ context.Context, _, cpf, name string) error {
	s.depositPayerCPF = cpf
	s.depositPayerName = name
	if s.deposit != nil {
		s.deposit.PayerCPF, s.deposit.PayerName = cpf, name
	}
	return nil
}
func (s *stubRepo) PutWithdrawal(_ context.Context, w *wallet.Withdrawal) error {
	s.withdrawals[w.WithdrawalID] = w
	return nil
}
func (s *stubRepo) GetWithdrawal(_ context.Context, id string) (*wallet.Withdrawal, error) {
	return s.withdrawals[id], nil
}
func (s *stubRepo) UpdateWithdrawal(_ context.Context, id string, updates map[string]any) error {
	if w, ok := s.withdrawals[id]; ok {
		if st, ok := updates["status"].(string); ok {
			w.Status = st
		}
	}
	return nil
}
func (s *stubRepo) ListProcessingWithdrawals(_ context.Context, _ int) ([]wallet.Withdrawal, error) {
	return nil, nil
}

type stubLocker struct{ busy bool }

func (l *stubLocker) Acquire(_ context.Context, _ string) (func(), bool, error) {
	if l.busy {
		return nil, false, nil
	}
	return func() {}, true, nil
}
func (l *stubLocker) AcquireOrdered(_ context.Context, _ ...string) (func(), bool, error) {
	if l.busy {
		return nil, false, nil
	}
	return func() {}, true, nil
}

type stubKYC struct {
	rec *kycclient.KYC
}

func (k *stubKYC) Get(_ context.Context, _ string) (*kycclient.KYC, error) { return k.rec, nil }

func newSvc(repo *stubRepo, locker *stubLocker, pc pix.PixClient, kyc KYCClient) *WalletService {
	return NewWalletService(repo, &stubUserRepo{}, &stubAudit{}, locker, pc, kyc)
}

func isProblem(t *testing.T, err error, wantType string) {
	t.Helper()
	p, ok := err.(*problem.Problem)
	if !ok {
		t.Fatalf("expected *problem.Problem, got %T: %v", err, err)
	}
	if p.Type != wantType {
		t.Fatalf("problem type = %q, want %q", p.Type, wantType)
	}
}

// --- tests ---

func TestConfirmDepositCreditsOnCPFMatch(t *testing.T) {
	repo := newStubRepo()
	repo.deposit = &wallet.PixDeposit{Txid: "tx1", WalletID: "w-real", UserID: "u1", AmountExpected: 5000, Status: wallet.DepositPending}
	fake := pix.NewFake()
	fake.StageCharge("tx1", 5000, pix.ChargeCompleted, "", "E2E-1")
	kyc := &stubKYC{rec: &kycclient.KYC{Level: "verified", CPF: "12345678901"}}
	svc := newSvc(repo, &stubLocker{}, fake, kyc)

	if err := svc.ConfirmDeposit(context.Background(), "tx1", "***456789**", "Someone"); err != nil {
		t.Fatalf("ConfirmDeposit: %v", err)
	}
	if repo.depositPayerCPF != "***456789**" {
		t.Errorf("payer CPF not persisted, got %q", repo.depositPayerCPF)
	}
	if len(repo.creditCalls) != 1 || repo.creditCalls[0].Amount != 5000 {
		t.Fatalf("expected one credit of 5000, got %+v", repo.creditCalls)
	}
	if repo.depositStatus != wallet.DepositConfirmed {
		t.Errorf("deposit status = %q, want confirmed", repo.depositStatus)
	}
	if len(fake.Refunds) != 0 {
		t.Errorf("no refund expected on match")
	}
}

func TestConfirmDepositRejectsAndRefundsOnCPFMismatch(t *testing.T) {
	repo := newStubRepo()
	repo.deposit = &wallet.PixDeposit{Txid: "tx1", WalletID: "w-real", UserID: "u1", AmountExpected: 5000, Status: wallet.DepositPending}
	fake := pix.NewFake()
	fake.StageCharge("tx1", 5000, pix.ChargeCompleted, "", "E2E-9")
	kyc := &stubKYC{rec: &kycclient.KYC{Level: "verified", CPF: "12345678901"}}
	svc := newSvc(repo, &stubLocker{}, fake, kyc)

	if err := svc.ConfirmDeposit(context.Background(), "tx1", "99999999999", "Other Guy"); err != nil {
		t.Fatalf("ConfirmDeposit: %v", err)
	}
	if len(repo.creditCalls) != 0 {
		t.Fatalf("no credit expected on CPF mismatch, got %+v", repo.creditCalls)
	}
	if repo.depositStatus != wallet.DepositRejectedCPF {
		t.Errorf("status = %q, want rejected_cpf_mismatch", repo.depositStatus)
	}
	if len(fake.Refunds) != 1 || fake.Refunds[0].Amount != 5000 {
		t.Errorf("expected one refund of 5000, got %+v", fake.Refunds)
	}
}

func TestConfirmDepositNoopWhenNotPaid(t *testing.T) {
	repo := newStubRepo()
	repo.deposit = &wallet.PixDeposit{Txid: "tx1", WalletID: "w-real", UserID: "u1", Status: wallet.DepositPending}
	fake := pix.NewFake()
	fake.StageCharge("tx1", 5000, pix.ChargeActive, "", "")
	svc := newSvc(repo, &stubLocker{}, fake, &stubKYC{rec: &kycclient.KYC{}})

	if err := svc.ConfirmDeposit(context.Background(), "tx1", "", ""); err != nil {
		t.Fatalf("ConfirmDeposit: %v", err)
	}
	if len(repo.creditCalls) != 0 {
		t.Errorf("no credit expected when charge not completed")
	}
}

// TestConfirmDepositRefundsExcessPayment covers two people scanning and
// paying the same QR code at once: only the first payment credits, the second
// is refunded straight to its own payer, and the deposit still confirms
// normally.
func TestConfirmDepositRefundsExcessPayment(t *testing.T) {
	repo := newStubRepo()
	repo.deposit = &wallet.PixDeposit{Txid: "tx1", WalletID: "w-real", UserID: "u1", AmountExpected: 5000, Status: wallet.DepositPending}
	fake := pix.NewFake()
	fake.StageCharge("tx1", 5000, pix.ChargeCompleted, "12345678901", "E2E-1")
	fake.StageChargePayment("tx1", "E2E-2", 5000, "99999999999")
	kyc := &stubKYC{rec: &kycclient.KYC{Level: "verified", CPF: "12345678901"}}
	svc := newSvc(repo, &stubLocker{}, fake, kyc)

	if err := svc.ConfirmDeposit(context.Background(), "tx1", "12345678901", "Someone"); err != nil {
		t.Fatalf("ConfirmDeposit: %v", err)
	}
	if repo.depositStatus != wallet.DepositConfirmed {
		t.Errorf("status = %q, want confirmed", repo.depositStatus)
	}
	if len(repo.creditCalls) != 1 || repo.creditCalls[0].Amount != 5000 {
		t.Fatalf("expected exactly one credit of 5000, got %+v", repo.creditCalls)
	}
	if len(fake.Refunds) != 1 || fake.Refunds[0].E2EID != "E2E-2" || fake.Refunds[0].Amount != 5000 {
		t.Fatalf("expected the excess payment refunded, got %+v", fake.Refunds)
	}
}

// TestConfirmDepositExcessPaymentRefundWebhookIsIdempotent covers the webhook
// Inter fires AGAIN once our own excess-payment refund (from
// TestConfirmDepositRefundsExcessPayment) completes: a devolução now shows on
// the excess payment's own entry. This second call must neither refund it a
// second time nor mistake it for a devolução on the credited (primary)
// payment and reverse the confirmed deposit.
func TestConfirmDepositExcessPaymentRefundWebhookIsIdempotent(t *testing.T) {
	repo := newStubRepo()
	repo.deposit = &wallet.PixDeposit{Txid: "tx1", WalletID: "w-real", UserID: "u1", AmountExpected: 5000, Status: wallet.DepositConfirmed, E2EID: "E2E-1"}
	fake := pix.NewFake()
	fake.StageCharge("tx1", 5000, pix.ChargeCompleted, "12345678901", "E2E-1")
	fake.StageChargePayment("tx1", "E2E-2", 5000, "99999999999")
	// The excess payment's own devolução already completed — as it would once
	// our earlier Refund() call for it settles at Inter.
	fake.Charges["tx1"].Payments[1].Refunds = []pix.Refund{{RtrID: "RTR-EXCESS", Amount: 5000, Status: pix.RefundCompleted}}
	svc := newSvc(repo, &stubLocker{}, fake, &stubKYC{rec: &kycclient.KYC{}})

	// A devolução-only webhook call carries no payer info.
	if err := svc.ConfirmDeposit(context.Background(), "tx1", "", ""); err != nil {
		t.Fatalf("ConfirmDeposit: %v", err)
	}
	if len(fake.Refunds) != 0 {
		t.Fatalf("excess payment already refunded, expected no new Refund() call, got %+v", fake.Refunds)
	}
	if len(repo.debitCalls) != 0 {
		t.Fatalf("the credited (primary) payment was never refunded, expected no reversal, got %+v", repo.debitCalls)
	}
	if repo.depositStatus != "" {
		t.Errorf("expected no status update at all, got %q", repo.depositStatus)
	}
}

// TestConfirmDepositRefundedBeforeConfirmNeverCredits covers a devolução that
// Inter already reports on the FIRST re-query that would otherwise confirm the
// deposit — money never enters the wallet, and the deposit is marked refunded
// directly rather than confirmed-then-reversed.
func TestConfirmDepositRefundedBeforeConfirmNeverCredits(t *testing.T) {
	repo := newStubRepo()
	repo.deposit = &wallet.PixDeposit{Txid: "tx1", WalletID: "w-real", UserID: "u1", AmountExpected: 5000, Status: wallet.DepositPending}
	fake := pix.NewFake()
	fake.StageCharge("tx1", 5000, pix.ChargeCompleted, "", "E2E-1")
	fake.StageChargeRefund("tx1", "RTR1", 5000, pix.RefundCompleted)
	kyc := &stubKYC{rec: &kycclient.KYC{Level: "verified", CPF: "12345678901"}}
	svc := newSvc(repo, &stubLocker{}, fake, kyc)

	if err := svc.ConfirmDeposit(context.Background(), "tx1", "12345678901", "Someone"); err != nil {
		t.Fatalf("ConfirmDeposit: %v", err)
	}
	if len(repo.creditCalls) != 0 {
		t.Fatalf("no credit expected, deposit was already refunded: %+v", repo.creditCalls)
	}
	if repo.depositStatus != wallet.DepositRefunded {
		t.Errorf("status = %q, want refunded", repo.depositStatus)
	}
}

// TestConfirmDepositRefundAfterConfirmReversesCredit covers a devolução that
// arrives on a LATER webhook call, after the deposit was already confirmed and
// credited — the credit must be taken back (Invariant 12: no money left in
// limbo).
func TestConfirmDepositRefundAfterConfirmReversesCredit(t *testing.T) {
	repo := newStubRepo()
	repo.deposit = &wallet.PixDeposit{Txid: "tx1", WalletID: "w-real", UserID: "u1", AmountExpected: 5000, Status: wallet.DepositConfirmed, E2EID: "E2E-1"}
	fake := pix.NewFake()
	fake.StageCharge("tx1", 5000, pix.ChargeCompleted, "", "E2E-1")
	fake.StageChargeRefund("tx1", "RTR1", 5000, pix.RefundCompleted)
	svc := newSvc(repo, &stubLocker{}, fake, &stubKYC{rec: &kycclient.KYC{}})

	// A devolução-only webhook call carries no payer info.
	if err := svc.ConfirmDeposit(context.Background(), "tx1", "", ""); err != nil {
		t.Fatalf("ConfirmDeposit: %v", err)
	}
	if len(repo.debitCalls) != 1 || repo.debitCalls[0].Amount != 5000 || repo.debitCalls[0].IdempotencyKey != "deposit-refund#RTR1" {
		t.Fatalf("expected one 5000 debit keyed by rtrId, got %+v", repo.debitCalls)
	}
	if repo.depositStatus != wallet.DepositRefunded {
		t.Errorf("status = %q, want refunded", repo.depositStatus)
	}
}

// TestConfirmDepositRefundReversalFailureFlagsRefundFailed covers the case
// where the user already spent the deposited money: the debit-back fails on
// insufficient balance, so the deposit is flagged for manual reconciliation
// instead of silently dropping the discrepancy (Invariant 12).
func TestConfirmDepositRefundReversalFailureFlagsRefundFailed(t *testing.T) {
	repo := newStubRepo()
	repo.deposit = &wallet.PixDeposit{Txid: "tx1", WalletID: "w-real", UserID: "u1", AmountExpected: 5000, Status: wallet.DepositConfirmed, E2EID: "E2E-1"}
	repo.debitErr = problem.InsufficientBalance()
	fake := pix.NewFake()
	fake.StageCharge("tx1", 5000, pix.ChargeCompleted, "", "E2E-1")
	fake.StageChargeRefund("tx1", "RTR1", 5000, pix.RefundCompleted)
	svc := newSvc(repo, &stubLocker{}, fake, &stubKYC{rec: &kycclient.KYC{}})

	err := svc.ConfirmDeposit(context.Background(), "tx1", "", "")
	if err == nil {
		t.Fatal("expected an error on refund-reversal debit failure")
	}
	if repo.depositStatus != wallet.DepositRefundFailed {
		t.Errorf("status = %q, want refund_failed", repo.depositStatus)
	}
}

// TestWithdrawUsesKYCCPFNotClientKey proves the destination PIX key sent to
// the bank is always the CPF from the caller's KYC record — the client has no
// way to influence it (there is no pixKey parameter anymore).
func TestWithdrawUsesKYCCPFNotClientKey(t *testing.T) {
	repo := newStubRepo()
	fake := pix.NewFake()
	kyc := &stubKYC{rec: &kycclient.KYC{Level: "verified", CPF: "12345678901"}}
	svc := newSvc(repo, &stubLocker{}, fake, kyc)

	w, err := svc.Withdraw(context.Background(), "u1", "verified", 5000, "idem-1")
	if err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	if len(fake.Transfers) != 1 || fake.Transfers[0].PixKey != "12345678901" {
		t.Fatalf("expected Transfer to the KYC CPF, got %+v", fake.Transfers)
	}
	if w.PixKey != "12345678901" {
		t.Errorf("withdrawal.PixKey = %q, want the KYC CPF", w.PixKey)
	}
}

// Regression: an unregistered PIX key (the CPF has no key at the bank) is a
// CLIENT error refunded immediately — it must never be reported as a 500, and
// it must never leave the withdrawal stuck in processing for reconciliation.
func TestWithdrawKeyNotFoundRefundsImmediately(t *testing.T) {
	repo := newStubRepo()
	fake := pix.NewFake()
	fake.TransferErr = pix.ErrKeyNotFound
	kyc := &stubKYC{rec: &kycclient.KYC{Level: "verified", CPF: "12345678901"}}
	svc := newSvc(repo, &stubLocker{}, fake, kyc)

	_, err := svc.Withdraw(context.Background(), "u1", "verified", 5000, "idem-1")
	isProblem(t, err, problem.TypePixKeyNotFound)

	p, _ := err.(*problem.Problem)
	if p.Status != 422 {
		t.Errorf("status = %d, want 422", p.Status)
	}
	if !repo.debitFeeCalled {
		t.Error("the debit still happens up front — it is the reversal that follows")
	}
	total := int64(5000) + wallet.WithdrawalFee(5000, nil)
	if len(repo.creditCalls) != 1 || repo.creditCalls[0].Amount != total || repo.creditCalls[0].EntryType != wallet.EntryReversal {
		t.Fatalf("expected one reversal credit of %d, got %+v", total, repo.creditCalls)
	}
	w := repo.withdrawals["withdraw#u1#idem-1"]
	if w == nil || w.Status != wallet.WithdrawReversed {
		t.Fatalf("expected withdrawal reversed, got %+v", w)
	}
}

func TestWithdrawBusy(t *testing.T) {
	svc := newSvc(newStubRepo(), &stubLocker{busy: true}, pix.NewFake(), &stubKYC{rec: &kycclient.KYC{CPF: "1"}})
	_, err := svc.Withdraw(context.Background(), "u1", "verified", 5000, "idem-1")
	isProblem(t, err, problem.TypeWalletBusy)
}

func TestWithdrawHappyPath(t *testing.T) {
	repo := newStubRepo()
	fake := pix.NewFake()
	kyc := &stubKYC{rec: &kycclient.KYC{Level: "verified", CPF: "12345678901"}}
	svc := newSvc(repo, &stubLocker{}, fake, kyc)

	w, err := svc.Withdraw(context.Background(), "u1", "verified", 5000, "idem-1")
	if err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	if !repo.debitFeeCalled {
		t.Fatal("expected debit-with-fee to be called")
	}
	if len(fake.Transfers) != 1 {
		t.Fatalf("expected one transfer, got %d", len(fake.Transfers))
	}
	if w.Status != wallet.WithdrawCompleted {
		t.Errorf("status = %q, want completed", w.Status)
	}
	if w.Fee != wallet.WithdrawalFee(5000, nil) {
		t.Errorf("fee = %d, want %d", w.Fee, wallet.WithdrawalFee(5000, nil))
	}
}

func TestWithdrawTransferFailureLeavesProcessing(t *testing.T) {
	repo := newStubRepo()
	fake := pix.NewFake()
	fake.TransferErr = errors.New("inter down")
	kyc := &stubKYC{rec: &kycclient.KYC{Level: "verified", CPF: "12345678901"}}
	svc := newSvc(repo, &stubLocker{}, fake, kyc)

	w, err := svc.Withdraw(context.Background(), "u1", "verified", 5000, "idem-1")
	if err != nil {
		t.Fatalf("Withdraw should not error on transfer failure: %v", err)
	}
	if w.Status != wallet.WithdrawProcessing {
		t.Errorf("status = %q, want processing", w.Status)
	}
}

func TestWithdrawIdempotentReplay(t *testing.T) {
	repo := newStubRepo()
	repo.withdrawals["withdraw#u1#idem-1"] = &wallet.Withdrawal{WithdrawalID: "withdraw#u1#idem-1", Status: wallet.WithdrawCompleted, Amount: 5000}
	svc := newSvc(repo, &stubLocker{}, pix.NewFake(), &stubKYC{rec: &kycclient.KYC{CPF: "1"}})

	w, err := svc.Withdraw(context.Background(), "u1", "verified", 5000, "idem-1")
	if err != nil {
		t.Fatalf("Withdraw replay: %v", err)
	}
	if w.Status != wallet.WithdrawCompleted || repo.debitFeeCalled {
		t.Errorf("replay should return existing without re-debit")
	}
}

// Sandbox is bought from the GAME wallet, never from `real`. If this ever debits
// w-real again, personal gambling limits are unenforceable: a user at their cap
// could buy sandbox straight from their real balance.
func TestPurchaseSandboxDebitsGameNotReal(t *testing.T) {
	repo := newStubRepo()
	svc := newSvc(repo, &stubLocker{}, pix.NewFake(), &stubKYC{rec: &kycclient.KYC{}})
	d, c, err := svc.PurchaseSandbox(context.Background(), "u1", 3000, "idem-1")
	if err != nil {
		t.Fatalf("PurchaseSandbox: %v", err)
	}
	if !repo.transferCalled || d.WalletID != "w-game" || c.WalletID != "w-sand" {
		t.Errorf("expected game→sandbox transfer, got d=%+v c=%+v", d, c)
	}
	if d.WalletID == "w-real" {
		t.Fatal("BYPASS: sandbox purchase debited the real wallet")
	}
}

func TestPurchaseSandboxRequiresActivation(t *testing.T) {
	repo := newStubRepo()
	repo.notActivated = true
	svc := newSvc(repo, &stubLocker{}, pix.NewFake(), &stubKYC{rec: &kycclient.KYC{}})

	_, _, err := svc.PurchaseSandbox(context.Background(), "u1", 3000, "idem-1")
	var p *problem.Problem
	if !errors.As(err, &p) || p.Type != problem.TypeGamblingNotActivated {
		t.Fatalf("PurchaseSandbox without activation = %v, want gambling-not-activated", err)
	}
	if repo.transferCalled {
		t.Fatal("a non-activated purchase must not touch any wallet")
	}
}

func TestFundGameMovesRealIntoGame(t *testing.T) {
	repo := newStubRepo()
	svc := newSvc(repo, &stubLocker{}, pix.NewFake(), &stubKYC{rec: &kycclient.KYC{}})
	d, c, err := svc.FundGame(context.Background(), "u1", 3000, "idem-1")
	if err != nil {
		t.Fatalf("FundGame: %v", err)
	}
	if d.WalletID != "w-real" || c.WalletID != "w-game" {
		t.Errorf("expected real→game transfer, got d=%+v c=%+v", d, c)
	}
	if d.Type != wallet.EntryGameFundDebit || c.Type != wallet.EntryGameFundCredit {
		t.Errorf("entry types = %q/%q, want %q/%q", d.Type, c.Type,
			wallet.EntryGameFundDebit, wallet.EntryGameFundCredit)
	}
}

func TestReturnFromGameMovesGameIntoReal(t *testing.T) {
	repo := newStubRepo()
	svc := newSvc(repo, &stubLocker{}, pix.NewFake(), &stubKYC{rec: &kycclient.KYC{}})
	d, c, err := svc.ReturnFromGame(context.Background(), "u1", 3000, "idem-1")
	if err != nil {
		t.Fatalf("ReturnFromGame: %v", err)
	}
	if d.WalletID != "w-game" || c.WalletID != "w-real" {
		t.Errorf("expected game→real transfer, got d=%+v c=%+v", d, c)
	}
	if d.Type != wallet.EntryGameReturnDebit || c.Type != wallet.EntryGameReturnCredit {
		t.Errorf("entry types = %q/%q, want %q/%q", d.Type, c.Type,
			wallet.EntryGameReturnDebit, wallet.EntryGameReturnCredit)
	}
}
