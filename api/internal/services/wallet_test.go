package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/kycclient"
	"gopkg.aoctech.app/wallet/api/internal/pix"
	"gopkg.aoctech.app/wallet/api/internal/problem"
	"gopkg.aoctech.app/wallet/api/internal/repositories"
)

// --- stubs ---

type stubRepo struct {
	real, game, sandbox wallet.Wallet
	notActivated        bool // no game/sandbox wallets — user never opted in
	sandboxCreated      bool // sandbox created via EnsureSandboxWallet despite notActivated
	deposit             *wallet.PixDeposit
	withdrawals         map[string]*wallet.Withdrawal
	depositStatus       string
	depositE2E          string
	depositPayerCPF     string
	depositPayerName    string
	creditCalls         []repositories.Mutation
	debitCalls          []repositories.Mutation
	debitErr            error
	transferErr         error
	transferCalled      bool
	holds               map[string]*wallet.Hold
	createHoldErr       error
	staleHolds          []wallet.Hold
	anyDebitSince       bool
}

func newStubRepo() *stubRepo {
	return &stubRepo{
		real:        wallet.Wallet{WalletID: "w-real", UserID: "u1", Type: wallet.TypeReal},
		game:        wallet.Wallet{WalletID: "w-game", UserID: "u1", Type: wallet.TypeGame},
		sandbox:     wallet.Wallet{WalletID: "w-sand", UserID: "u1", Type: wallet.TypeSandbox},
		withdrawals: map[string]*wallet.Withdrawal{},
		holds:       map[string]*wallet.Hold{},
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
		var sandbox *wallet.Wallet
		if s.sandboxCreated {
			sandbox = &s.sandbox
		}
		return &s.real, nil, sandbox, nil
	}
	return &s.real, &s.game, &s.sandbox, nil
}
func (s *stubRepo) EnsureSandboxWallet(_ context.Context, _ string) (*wallet.Wallet, error) {
	s.sandboxCreated = true
	return &s.sandbox, nil
}
func (s *stubRepo) Credit(_ context.Context, m repositories.Mutation, _ ...types.TransactWriteItem) (*wallet.LedgerEntry, bool, error) {
	s.creditCalls = append(s.creditCalls, m)
	entry := &wallet.LedgerEntry{WalletID: m.WalletID, SK: "ledger#credit", Amount: m.Amount, Type: m.EntryType}
	if m.Extra != nil {
		m.Extra(entry)
	}
	return entry, false, nil
}
func (s *stubRepo) Debit(_ context.Context, m repositories.Mutation, _ ...types.TransactWriteItem) (*wallet.LedgerEntry, bool, error) {
	s.debitCalls = append(s.debitCalls, m)
	if s.debitErr != nil {
		return nil, false, s.debitErr
	}
	entry := &wallet.LedgerEntry{WalletID: m.WalletID, SK: "ledger#debit", Amount: -m.Amount, Type: m.EntryType}
	if m.Extra != nil {
		m.Extra(entry)
	}
	return entry, false, nil
}
func (s *stubRepo) ConfirmDepositCredit(ctx context.Context, m repositories.Mutation, _ string, e2eID string, _ ...types.TransactWriteItem) (*wallet.LedgerEntry, bool, error) {
	entry, replayed, err := s.Credit(ctx, m)
	if err == nil {
		s.depositStatus, s.depositE2E = wallet.DepositConfirmed, e2eID
		if s.deposit != nil {
			s.deposit.Status = wallet.DepositConfirmed
		}
	}
	return entry, replayed, err
}
func (s *stubRepo) FindMutation(_ context.Context, idemKey, _ string) (*wallet.LedgerEntry, error) {
	for i := range s.creditCalls {
		if s.creditCalls[i].IdempotencyKey == idemKey {
			return &wallet.LedgerEntry{Amount: s.creditCalls[i].Amount}, nil
		}
	}
	return nil, nil
}
func (s *stubRepo) Transfer(_ context.Context, from, to string, amount, creditAmount int64, dt, ct, _, _, _ string, _ ...types.TransactWriteItem) (*wallet.LedgerEntry, *wallet.LedgerEntry, bool, error) {
	s.transferCalled = true
	if s.transferErr != nil {
		return nil, nil, false, s.transferErr
	}
	return &wallet.LedgerEntry{WalletID: from, Amount: -amount, Type: dt}, &wallet.LedgerEntry{WalletID: to, Amount: creditAmount, Type: ct}, false, nil
}
func (s *stubRepo) Statement(_ context.Context, _ string, _ int, _ map[string]types.AttributeValue) (*repositories.QueryResult, error) {
	return &repositories.QueryResult{}, nil
}
func (s *stubRepo) GetDeposit(_ context.Context, _ string) (*wallet.PixDeposit, error) {
	return s.deposit, nil
}
func (s *stubRepo) PutDepositIfAbsent(_ context.Context, d *wallet.PixDeposit) error {
	if s.deposit != nil {
		return repositories.ErrDepositExists
	}
	s.deposit = d
	return nil
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

// WithdrawalPutTx records the row as if the transaction it joins had committed,
// so replay lookups (GetWithdrawal) see it.
func (s *stubRepo) WithdrawalPutTx(w *wallet.Withdrawal) (types.TransactWriteItem, error) {
	s.withdrawals[w.WithdrawalID] = w
	return types.TransactWriteItem{}, nil
}

// MarkWithdrawalReversed mirrors the real transition (extras are the caller's
// counter writes, already recorded by the stub user repo when built).
func (s *stubRepo) MarkWithdrawalReversed(_ context.Context, id string, _ ...types.TransactWriteItem) error {
	if w, ok := s.withdrawals[id]; ok {
		w.Status = wallet.WithdrawReversed
	}
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
func (s *stubRepo) ListPendingDepositsOlderThan(_ context.Context, _ time.Time, _ int) ([]wallet.PixDeposit, error) {
	return nil, nil
}
func (s *stubRepo) CreateHold(_ context.Context, holdID, walletID, userID string, amount int64, tableRef, _, _ string) (*wallet.Hold, bool, error) {
	if existing, ok := s.holds[holdID]; ok {
		return existing, true, nil
	}
	if s.createHoldErr != nil {
		return nil, false, s.createHoldErr
	}
	h := &wallet.Hold{
		HoldID: holdID, WalletID: walletID, UserID: userID, Amount: amount,
		TableRef: tableRef, Status: wallet.HoldHeld,
	}
	s.holds[holdID] = h
	return h, false, nil
}
func (s *stubRepo) GetHold(_ context.Context, holdID string) (*wallet.Hold, error) {
	return s.holds[holdID], nil
}
func (s *stubRepo) UpdateHoldStatus(_ context.Context, holdID, fromStatus, toStatus string) (bool, error) {
	h, ok := s.holds[holdID]
	if !ok || h.Status != fromStatus {
		return false, nil
	}
	h.Status = toStatus
	return true, nil
}
func (s *stubRepo) ReleaseHoldAtomic(_ context.Context, h *wallet.Hold, _, _ string) (*wallet.Hold, bool, error) {
	if h.Status != wallet.HoldHeld {
		return h, true, nil
	}
	h.Status = wallet.HoldReleased
	s.creditCalls = append(s.creditCalls, repositories.Mutation{WalletID: h.WalletID, Amount: h.Amount, EntryType: wallet.EntryGameHoldRelease})
	if s.holds != nil {
		s.holds[h.HoldID] = h
	}
	return h, false, nil
}
func (s *stubRepo) CashoutHoldsAtomic(_ context.Context, walletID, _ string, amount int64, tableRef string, holds []*wallet.Hold, _, _ string) (*wallet.LedgerEntry, bool, error) {
	for _, h := range holds {
		h.Status = wallet.HoldSettled
	}
	return &wallet.LedgerEntry{WalletID: walletID, Amount: amount, Type: wallet.EntryGameCashoutCredit, Ref: tableRef}, false, nil
}
func (s *stubRepo) ScanStaleHolds(_ context.Context, _ time.Time, _ int) ([]wallet.Hold, error) {
	return s.staleHolds, nil
}
func (s *stubRepo) ListOpenHoldsForWallet(_ context.Context, walletID string, _ int) ([]wallet.Hold, error) {
	var out []wallet.Hold
	for _, h := range s.holds {
		if h.WalletID == walletID && h.Status == wallet.HoldHeld {
			out = append(out, *h)
		}
	}
	return out, nil
}
func (s *stubRepo) AnyDebitSince(_ context.Context, _, _ string) (bool, error) {
	return s.anyDebitSince, nil
}

func (s *stubRepo) TransitionDepositStatus(_ context.Context, _ string, fromStatus, toStatus, e2e string) (bool, error) {
	current := s.depositStatus
	if s.deposit != nil {
		current = s.deposit.Status
	}
	if current != fromStatus {
		return false, nil
	}
	s.depositStatus, s.depositE2E = toStatus, e2e
	if s.deposit != nil {
		s.deposit.Status = toStatus
		s.deposit.E2EID = e2e
	}
	return true, nil
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

// fakeDepositBaas is a minimal BaasProvider for InitiateDeposit/ConfirmDeposit
// custody-branch tests — approved/notApproved toggles GetIfApproved/GetAccount,
// createCalls records CreateDepositCharge invocations.

func (s *stubRepo) ListRefundableDepositsOlderThan(_ context.Context, _ time.Time, _ int) ([]wallet.PixDeposit, error) {
	if s.deposit == nil {
		return nil, nil
	}
	switch s.deposit.Status {
	case wallet.DepositRefundPending, wallet.DepositRefundFailed, wallet.DepositRejectedCPF:
		return []wallet.PixDeposit{*s.deposit}, nil
	default:
		return nil, nil
	}
}

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
	kyc := &stubKYC{rec: &kycclient.KYC{Level: "enhanced", CPF: "12345678901"}}
	svc := newSvc(repo, &stubLocker{}, fake, kyc)

	if err := svc.ConfirmDeposit(context.Background(), "tx1", "***456789**", "Someone", false); err != nil {
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
	kyc := &stubKYC{rec: &kycclient.KYC{Level: "enhanced", CPF: "12345678901"}}
	svc := newSvc(repo, &stubLocker{}, fake, kyc)

	if err := svc.ConfirmDeposit(context.Background(), "tx1", "99999999999", "Other Guy", false); err != nil {
		t.Fatalf("ConfirmDeposit: %v", err)
	}
	if len(repo.creditCalls) != 0 {
		t.Fatalf("no credit expected on CPF mismatch, got %+v", repo.creditCalls)
	}
	if repo.depositStatus != wallet.DepositRefunded {
		t.Errorf("status = %q, want refunded", repo.depositStatus)
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

	if err := svc.ConfirmDeposit(context.Background(), "tx1", "", "", false); err != nil {
		t.Fatalf("ConfirmDeposit: %v", err)
	}
	if len(repo.creditCalls) != 0 {
		t.Errorf("no credit expected when charge not completed")
	}
}

func TestConfirmDepositSweepNeverCreditsWithoutPayerIdentity(t *testing.T) {
	repo := newStubRepo()
	repo.deposit = &wallet.PixDeposit{Txid: "tx1", WalletID: "w-real", UserID: "u1", AmountExpected: 5000, Status: wallet.DepositPending}
	fake := pix.NewFake()
	fake.StageCharge("tx1", 5000, pix.ChargeCompleted, "", "E2E-1")
	svc := newSvc(repo, &stubLocker{}, fake, &stubKYC{rec: &kycclient.KYC{CPF: "12345678901"}})

	if err := svc.ConfirmDeposit(context.Background(), "tx1", "", "", true); err == nil {
		t.Fatal("paid deposit without payer identity must remain quarantined")
	}
	if len(repo.creditCalls) != 0 || repo.depositStatus == wallet.DepositConfirmed {
		t.Fatalf("payer-less sweep must never credit: credits=%v status=%q", repo.creditCalls, repo.depositStatus)
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
	kyc := &stubKYC{rec: &kycclient.KYC{Level: "enhanced", CPF: "12345678901"}}
	svc := newSvc(repo, &stubLocker{}, fake, kyc)

	if err := svc.ConfirmDeposit(context.Background(), "tx1", "12345678901", "Someone", false); err != nil {
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
	if err := svc.ConfirmDeposit(context.Background(), "tx1", "", "", false); err != nil {
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
	kyc := &stubKYC{rec: &kycclient.KYC{Level: "enhanced", CPF: "12345678901"}}
	svc := newSvc(repo, &stubLocker{}, fake, kyc)

	if err := svc.ConfirmDeposit(context.Background(), "tx1", "12345678901", "Someone", false); err != nil {
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
	if err := svc.ConfirmDeposit(context.Background(), "tx1", "", "", false); err != nil {
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

	err := svc.ConfirmDeposit(context.Background(), "tx1", "", "", false)
	if err == nil {
		t.Fatal("expected an error on refund-reversal debit failure")
	}
	if repo.depositStatus != wallet.DepositRefundFailed {
		t.Errorf("status = %q, want refund_failed", repo.depositStatus)
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

func TestCreditSandboxWorksWithoutActivation(t *testing.T) {
	repo := newStubRepo()
	repo.notActivated = true
	svc := newSvc(repo, &stubLocker{}, pix.NewFake(), &stubKYC{rec: &kycclient.KYC{}})

	entry, err := svc.CreditSandbox(context.Background(), "u1", 500, "idem-1", "daily-reward", "")
	if err != nil {
		t.Fatalf("CreditSandbox without activation = %v, want success", err)
	}
	if entry.WalletID != "w-sand" || entry.Amount != 500 {
		t.Errorf("entry = %+v, want wallet w-sand amount 500", entry)
	}
	if !repo.sandboxCreated {
		t.Fatal("expected sandbox wallet to be lazily created")
	}
}

func TestDebitSandboxWorksWithoutActivation(t *testing.T) {
	repo := newStubRepo()
	repo.notActivated = true
	svc := newSvc(repo, &stubLocker{}, pix.NewFake(), &stubKYC{rec: &kycclient.KYC{}})

	entry, err := svc.DebitSandbox(context.Background(), "u1", 200, "idem-2", "bet", "")
	if err != nil {
		t.Fatalf("DebitSandbox without activation = %v, want success", err)
	}
	if entry.WalletID != "w-sand" || entry.Amount != -200 {
		t.Errorf("entry = %+v, want wallet w-sand amount -200 (debit)", entry)
	}
}

// FundGame/ReturnFromGame/PurchaseSandbox/HoldGame remain gated — this change
// must not weaken the game wallet's KYC/consent gate.
func TestFundGameStillRequiresActivation(t *testing.T) {
	repo := newStubRepo()
	repo.notActivated = true
	users := &stubUserRepo{user: &wallet.User{GameLimits: &wallet.GameLimits{Daily: 10000, Weekly: 10000, Monthly: 10000}}}
	svc := NewWalletService(repo, users, &stubAudit{}, &stubLocker{}, pix.NewFake(), &stubKYC{rec: &kycclient.KYC{}})

	_, _, err := svc.FundGame(context.Background(), "u1", 3000, "idem-3")
	isProblem(t, err, problem.TypeGamblingNotActivated)
}

func TestReturnFromGameStillRequiresActivation(t *testing.T) {
	repo := newStubRepo()
	repo.notActivated = true
	svc := newSvc(repo, &stubLocker{}, pix.NewFake(), &stubKYC{rec: &kycclient.KYC{}})

	_, _, err := svc.ReturnFromGame(context.Background(), "u1", 3000, "idem-4")
	isProblem(t, err, problem.TypeGamblingNotActivated)
}

func TestBalancesForReturnsZeroForBrandNewUser(t *testing.T) {
	repo := newStubRepo()
	repo.notActivated = true
	svc := newSvc(repo, &stubLocker{}, pix.NewFake(), &stubKYC{rec: &kycclient.KYC{}})

	got, err := svc.BalancesFor(context.Background(), "u1")
	if err != nil {
		t.Fatalf("BalancesFor: %v", err)
	}
	if got.GameBalance != 0 || got.SandboxBalance != 0 {
		t.Fatalf("balances = %+v, want zero/zero for a user with no wallets", got)
	}
}

func TestBalancesForReturnsActualBalances(t *testing.T) {
	repo := newStubRepo()
	repo.game.Balance = 1500
	repo.sandbox.Balance = 30000
	svc := newSvc(repo, &stubLocker{}, pix.NewFake(), &stubKYC{rec: &kycclient.KYC{}})

	got, err := svc.BalancesFor(context.Background(), "u1")
	if err != nil {
		t.Fatalf("BalancesFor: %v", err)
	}
	if got.GameBalance != 1500 || got.SandboxBalance != 30000 {
		t.Fatalf("balances = %+v, want game=1500 sandbox=30000", got)
	}
}

func TestBalancesForSandboxOnlyUser(t *testing.T) {
	repo := newStubRepo()
	repo.notActivated = true
	repo.sandboxCreated = true
	repo.sandbox.Balance = 700
	svc := newSvc(repo, &stubLocker{}, pix.NewFake(), &stubKYC{rec: &kycclient.KYC{}})

	got, err := svc.BalancesFor(context.Background(), "u1")
	if err != nil {
		t.Fatalf("BalancesFor: %v", err)
	}
	if got.GameBalance != 0 || got.SandboxBalance != 700 {
		t.Fatalf("balances = %+v, want game=0 (never activated) sandbox=700", got)
	}
}

func TestFundGameMovesRealIntoGame(t *testing.T) {
	repo := newStubRepo()
	// FundGame now requires configured personal limits (responsible gambling).
	users := &stubUserRepo{user: &wallet.User{GameLimits: &wallet.GameLimits{Daily: 10000, Weekly: 10000, Monthly: 10000}}}
	svc := NewWalletService(repo, users, &stubAudit{}, &stubLocker{}, pix.NewFake(), &stubKYC{rec: &kycclient.KYC{}})
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

func TestDebitRealHappyPath(t *testing.T) {
	repo := newStubRepo()
	svc := newSvc(repo, &stubLocker{}, pix.NewFake(), &stubKYC{rec: &kycclient.KYC{CPF: "1"}})
	entry, err := svc.DebitReal(context.Background(), "u1", 5000, "charge-1", "subscription", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entry.WalletID != "w-real" {
		t.Fatalf("debited wallet = %q, want w-real", entry.WalletID)
	}
	if entry.Amount != -5000 {
		t.Fatalf("entry amount = %d, want -5000", entry.Amount)
	}
	if entry.Type != wallet.EntryBillingDebit {
		t.Fatalf("entry type = %q, want %q", entry.Type, wallet.EntryBillingDebit)
	}
}

func TestDebitRealInsufficientBalance(t *testing.T) {
	repo := newStubRepo()
	repo.debitErr = problem.InsufficientBalance()
	svc := newSvc(repo, &stubLocker{}, pix.NewFake(), &stubKYC{rec: &kycclient.KYC{CPF: "1"}})
	_, err := svc.DebitReal(context.Background(), "u1", 5000, "charge-1", "subscription", "")
	isProblem(t, err, problem.TypeInsufficientBalance)
}

func TestDebitRealWalletBusy(t *testing.T) {
	svc := newSvc(newStubRepo(), &stubLocker{busy: true}, pix.NewFake(), &stubKYC{rec: &kycclient.KYC{CPF: "1"}})
	_, err := svc.DebitReal(context.Background(), "u1", 5000, "charge-1", "subscription", "")
	isProblem(t, err, problem.TypeWalletBusy)
}

// --- game wallet holds (skill-game integration, e.g. ctech-poker) ---

func TestHoldGameHappyPath(t *testing.T) {
	repo := newStubRepo()
	svc := newSvc(repo, &stubLocker{}, pix.NewFake(), &stubKYC{rec: &kycclient.KYC{}})
	h, err := svc.HoldGame(context.Background(), "u1", 5000, "table-1:seat-2", "idem-1")
	if err != nil {
		t.Fatalf("HoldGame: %v", err)
	}
	if h.WalletID != "w-game" || h.Amount != 5000 || h.Status != wallet.HoldHeld {
		t.Fatalf("unexpected hold: %+v", h)
	}

	// Idempotent replay: same key returns the same hold, no second reservation.
	h2, err := svc.HoldGame(context.Background(), "u1", 5000, "table-1:seat-2", "idem-1")
	if err != nil {
		t.Fatalf("HoldGame replay: %v", err)
	}
	if h2.HoldID != h.HoldID {
		t.Fatalf("replay created a new hold: %s vs %s", h2.HoldID, h.HoldID)
	}
}

func TestHoldGameInsufficientBalance(t *testing.T) {
	repo := newStubRepo()
	repo.createHoldErr = problem.InsufficientBalance()
	svc := newSvc(repo, &stubLocker{}, pix.NewFake(), &stubKYC{rec: &kycclient.KYC{}})
	_, err := svc.HoldGame(context.Background(), "u1", 5000, "table-1", "idem-1")
	isProblem(t, err, problem.TypeInsufficientBalance)
}

func TestHoldGameRequiresActivation(t *testing.T) {
	repo := newStubRepo()
	repo.notActivated = true
	svc := newSvc(repo, &stubLocker{}, pix.NewFake(), &stubKYC{rec: &kycclient.KYC{}})
	_, err := svc.HoldGame(context.Background(), "u1", 5000, "table-1", "idem-1")
	isProblem(t, err, problem.TypeGamblingNotActivated)
}

func TestHoldGameWalletBusy(t *testing.T) {
	svc := newSvc(newStubRepo(), &stubLocker{busy: true}, pix.NewFake(), &stubKYC{rec: &kycclient.KYC{}})
	_, err := svc.HoldGame(context.Background(), "u1", 5000, "table-1", "idem-1")
	isProblem(t, err, problem.TypeWalletBusy)
}

func TestReleaseHoldRefundsFullAmount(t *testing.T) {
	repo := newStubRepo()
	svc := newSvc(repo, &stubLocker{}, pix.NewFake(), &stubKYC{rec: &kycclient.KYC{}})
	h, err := svc.HoldGame(context.Background(), "u1", 5000, "table-1", "idem-hold")
	if err != nil {
		t.Fatalf("HoldGame: %v", err)
	}

	released, err := svc.ReleaseHold(context.Background(), "u1", h.HoldID, "idem-release")
	if err != nil {
		t.Fatalf("ReleaseHold: %v", err)
	}
	if released.Status != wallet.HoldReleased {
		t.Fatalf("status = %q, want released", released.Status)
	}
	if len(repo.creditCalls) != 1 || repo.creditCalls[0].Amount != 5000 || repo.creditCalls[0].EntryType != wallet.EntryGameHoldRelease {
		t.Fatalf("expected one release credit of 5000, got %+v", repo.creditCalls)
	}

	// Already released — a retry is a benign no-op, not an error, and must not
	// credit a second time.
	released2, err := svc.ReleaseHold(context.Background(), "u1", h.HoldID, "idem-release-2")
	if err != nil {
		t.Fatalf("ReleaseHold retry: %v", err)
	}
	if released2.Status != wallet.HoldReleased {
		t.Fatalf("retry status = %q, want released", released2.Status)
	}
	if len(repo.creditCalls) != 1 {
		t.Fatalf("retry must not credit again, got %d credits", len(repo.creditCalls))
	}
}

func TestReleaseHoldNotFound(t *testing.T) {
	repo := newStubRepo()
	svc := newSvc(repo, &stubLocker{}, pix.NewFake(), &stubKYC{rec: &kycclient.KYC{}})
	_, err := svc.ReleaseHold(context.Background(), "u1", "hold#missing", "idem-1")
	isProblem(t, err, problem.TypeNotFound)
}

// A cashout may span several holds but cannot exceed their aggregate value.
func TestCashoutGameMayConsumeMultipleHolds(t *testing.T) {
	repo := newStubRepo()
	svc := newSvc(repo, &stubLocker{}, pix.NewFake(), &stubKYC{rec: &kycclient.KYC{}})

	hA, err := svc.HoldGame(context.Background(), "u1", 10000, "table-1", "idem-a")
	if err != nil {
		t.Fatalf("HoldGame A: %v", err)
	}
	hB, err := svc.HoldGame(context.Background(), "u1", 10000, "table-1", "idem-b")
	if err != nil {
		t.Fatalf("HoldGame B: %v", err)
	}

	// Player A wins the whole 20000 pot — double either single hold's amount.
	entry, err := svc.CashoutGame(context.Background(), "u1", 20000, "table-1", []string{hA.HoldID, hB.HoldID}, "idem-cashout")
	if err != nil {
		t.Fatalf("CashoutGame: %v", err)
	}
	if entry.Amount != 20000 || entry.Type != wallet.EntryGameCashoutCredit {
		t.Fatalf("unexpected cashout entry: %+v", entry)
	}

	for _, id := range []string{hA.HoldID, hB.HoldID} {
		h, err := repo.GetHold(context.Background(), id)
		if err != nil {
			t.Fatalf("GetHold: %v", err)
		}
		if h.Status != wallet.HoldSettled {
			t.Fatalf("hold %s status = %q, want settled", id, h.Status)
		}
	}
}

func TestCashoutGameCannotMintBeyondReservedHolds(t *testing.T) {
	repo := newStubRepo()
	svc := newSvc(repo, &stubLocker{}, pix.NewFake(), &stubKYC{rec: &kycclient.KYC{}})
	h, err := svc.HoldGame(context.Background(), "u1", 5000, "table-1", "idem-hold")
	if err != nil {
		t.Fatalf("HoldGame: %v", err)
	}
	_, err = svc.CashoutGame(context.Background(), "u1", 5001, "table-1", []string{h.HoldID}, "idem-cashout")
	isProblem(t, err, problem.TypeBadRequest)
	if h.Status != wallet.HoldHeld {
		t.Fatalf("rejected cashout consumed hold: %s", h.Status)
	}
}

func TestCashoutGameRejectsAlreadyConsumedHold(t *testing.T) {
	repo := newStubRepo()
	svc := newSvc(repo, &stubLocker{}, pix.NewFake(), &stubKYC{rec: &kycclient.KYC{}})
	h, err := svc.HoldGame(context.Background(), "u1", 5000, "table-1", "idem-hold")
	if err != nil {
		t.Fatalf("HoldGame: %v", err)
	}
	// Simulate a prior partial failure: the hold was already settled by an
	// earlier, since-crashed cash-out attempt.
	if _, err := repo.UpdateHoldStatus(context.Background(), h.HoldID, wallet.HoldHeld, wallet.HoldSettled); err != nil {
		t.Fatalf("UpdateHoldStatus: %v", err)
	}

	_, err = svc.CashoutGame(context.Background(), "u1", 5000, "table-1", []string{h.HoldID}, "idem-cashout-retry")
	isProblem(t, err, problem.TypeConflict)
}
