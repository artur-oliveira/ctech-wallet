package services

import (
	"context"
	"errors"
	"strings"
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
	creditCalls         []repositories.Mutation
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
	rec        *kycclient.KYC
	confirmErr error
	confirmed  bool
}

func (k *stubKYC) Confirm(_ context.Context, _, _ string) error {
	k.confirmed = true
	return k.confirmErr
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
	fake.StageCharge("tx1", 5000, pix.ChargeCompleted, "12345678901", "E2E-1")
	kyc := &stubKYC{rec: &kycclient.KYC{Level: "basic", CPF: "12345678901"}}
	svc := newSvc(repo, &stubLocker{}, fake, kyc)

	if err := svc.ConfirmDeposit(context.Background(), "tx1"); err != nil {
		t.Fatalf("ConfirmDeposit: %v", err)
	}
	if len(repo.creditCalls) != 1 || repo.creditCalls[0].Amount != 5000 {
		t.Fatalf("expected one credit of 5000, got %+v", repo.creditCalls)
	}
	if repo.depositStatus != wallet.DepositConfirmed {
		t.Errorf("deposit status = %q, want confirmed", repo.depositStatus)
	}
	if !kyc.confirmed {
		t.Errorf("expected KYC confirm on basic-level first deposit")
	}
	if len(fake.Refunds) != 0 {
		t.Errorf("no refund expected on match")
	}
}

func TestConfirmDepositRejectsAndRefundsOnCPFMismatch(t *testing.T) {
	repo := newStubRepo()
	repo.deposit = &wallet.PixDeposit{Txid: "tx1", WalletID: "w-real", UserID: "u1", AmountExpected: 5000, Status: wallet.DepositPending}
	fake := pix.NewFake()
	fake.StageCharge("tx1", 5000, pix.ChargeCompleted, "99999999999", "E2E-9")
	kyc := &stubKYC{rec: &kycclient.KYC{Level: "verified", CPF: "12345678901"}}
	svc := newSvc(repo, &stubLocker{}, fake, kyc)

	if err := svc.ConfirmDeposit(context.Background(), "tx1"); err != nil {
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

	if err := svc.ConfirmDeposit(context.Background(), "tx1"); err != nil {
		t.Fatalf("ConfirmDeposit: %v", err)
	}
	if len(repo.creditCalls) != 0 {
		t.Errorf("no credit expected when charge not completed")
	}
}

func TestWithdrawCPFMismatch(t *testing.T) {
	repo := newStubRepo()
	fake := pix.NewFake()
	fake.StageDict("key@x", "99999999999", "Someone Else")
	kyc := &stubKYC{rec: &kycclient.KYC{Level: "verified", CPF: "12345678901"}}
	svc := newSvc(repo, &stubLocker{}, fake, kyc)

	_, err := svc.Withdraw(context.Background(), "u1", "verified", 5000, "key@x", "idem-1")
	isProblem(t, err, problem.TypeWithdrawCPFMismatch)
	if repo.debitFeeCalled {
		t.Error("no debit expected on CPF mismatch")
	}
}

// Regression: an unregistered/mistyped PIX key is a CLIENT error. It used to be
// reported as a 500 with the bank's raw error leaked into the detail.
func TestWithdrawUnknownPixKeyIs422NotServerError(t *testing.T) {
	repo := newStubRepo()
	fake := pix.NewFake() // no DICT entry staged → key not found
	kyc := &stubKYC{rec: &kycclient.KYC{Level: "verified", CPF: "12345678901"}}
	svc := newSvc(repo, &stubLocker{}, fake, kyc)

	_, err := svc.Withdraw(context.Background(), "u1", "verified", 5000, "typo@pix", "idem-1")
	isProblem(t, err, problem.TypePixKeyNotFound)

	p, _ := err.(*problem.Problem)
	if p.Status != 422 {
		t.Errorf("status = %d, want 422", p.Status)
	}
	if strings.Contains(p.Detail, "dict") || strings.Contains(p.Detail, "not found") {
		t.Errorf("detail leaks internals: %q", p.Detail)
	}
	if repo.debitFeeCalled {
		t.Error("no debit expected when the PIX key is unknown")
	}
}

func TestWithdrawBusy(t *testing.T) {
	svc := newSvc(newStubRepo(), &stubLocker{busy: true}, pix.NewFake(), &stubKYC{rec: &kycclient.KYC{CPF: "1"}})
	_, err := svc.Withdraw(context.Background(), "u1", "verified", 5000, "key@x", "idem-1")
	isProblem(t, err, problem.TypeWalletBusy)
}

func TestWithdrawHappyPath(t *testing.T) {
	repo := newStubRepo()
	fake := pix.NewFake()
	fake.StageDict("key@x", "12345678901", "Me")
	kyc := &stubKYC{rec: &kycclient.KYC{Level: "verified", CPF: "12345678901"}}
	svc := newSvc(repo, &stubLocker{}, fake, kyc)

	w, err := svc.Withdraw(context.Background(), "u1", "verified", 5000, "key@x", "idem-1")
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
	fake.StageDict("key@x", "12345678901", "Me")
	fake.TransferErr = errors.New("inter down")
	kyc := &stubKYC{rec: &kycclient.KYC{Level: "verified", CPF: "12345678901"}}
	svc := newSvc(repo, &stubLocker{}, fake, kyc)

	w, err := svc.Withdraw(context.Background(), "u1", "verified", 5000, "key@x", "idem-1")
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

	w, err := svc.Withdraw(context.Background(), "u1", "verified", 5000, "key@x", "idem-1")
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
