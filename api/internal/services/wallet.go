// Package services holds the wallet business logic. It orchestrates the
// repository (atomic ledger), the per-wallet lock, the PIX partner bank, and the
// account KYC client, upholding the Financial Safety Invariants.
package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/google/uuid"

	"gopkg.aoctech.app/wallet/api/internal/domain/id"
	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/kycclient"
	"gopkg.aoctech.app/wallet/api/internal/pix"
	"gopkg.aoctech.app/wallet/api/internal/problem"
	"gopkg.aoctech.app/wallet/api/internal/repositories"
)

// depositTTLMinutes is the DynamoDB TTL lifetime of a pending PIX charge row.
// It MUST be longer than Inter's actual charge validity AND longer than the
// reconcile sweep interval, so a pending deposit is always re-queried (and
// credited or refunded) before the row is silently TTL-deleted. Previously 5m —
// shorter than both Inter's validity and a realistic sweep interval, so a
// payment landing late was lost (SEC-02). 60m gives the sweep (see
// sweepAgeThreshold) a 50m window to run before the row disappears.
const depositTTLMinutes = 60

// interWithdrawalNamespace namespaces the deterministic UUID sent to Inter as
// x-id-idempotente for PIX payouts (Inter rejects any other format). Derived
// via UUID v5 from withdrawalID, so it's stable across the initial Transfer
// call and every later reconciliation QueryTransfer for the same withdrawal.
// DO NOT EVER CHANGE
var interWithdrawalNamespace = uuid.MustParse("6f9c3b8e-6b0a-4b7e-9c1a-2f6f6e6f0a1a")

func interIdemKey(withdrawalID string) string {
	return uuid.NewSHA1(interWithdrawalNamespace, []byte(withdrawalID)).String()
}

// Repo is the persistence surface the service depends on (dependency inversion
// so the service is unit-testable without DynamoDB).
type Repo interface {
	GetWallet(ctx context.Context, walletID string) (*wallet.Wallet, error)
	EnsureRealWallet(ctx context.Context, userID string) (*wallet.Wallet, error)
	EnsureSandboxWallet(ctx context.Context, userID string) (*wallet.Wallet, error)
	EnsureGamblingWallets(ctx context.Context, userID string) (game, sandbox *wallet.Wallet, err error)
	LoadWallets(ctx context.Context, userID string) (real, game, sandbox *wallet.Wallet, err error)
	Credit(ctx context.Context, m repositories.Mutation) (*wallet.LedgerEntry, bool, error)
	Debit(ctx context.Context, m repositories.Mutation) (*wallet.LedgerEntry, bool, error)
	DebitWithFee(ctx context.Context, w *wallet.Withdrawal, amount, fee int64, idemKey, reqHash string) (*wallet.LedgerEntry, *wallet.LedgerEntry, bool, error)
	Transfer(ctx context.Context, from, to string, amount, creditAmount int64, debitType, creditType, ref, idemKey, reqHash string, extra ...types.TransactWriteItem) (*wallet.LedgerEntry, *wallet.LedgerEntry, bool, error)
	Statement(ctx context.Context, walletID string, limit int, startKey map[string]types.AttributeValue) (*repositories.QueryResult, error)
	PutDeposit(ctx context.Context, d *wallet.PixDeposit) error
	GetDeposit(ctx context.Context, txid string) (*wallet.PixDeposit, error)
	ReserveDepositIdem(ctx context.Context, guardPK, txid, userID, reqHash string) (reservedTxid string, existing *wallet.PixDeposit, conflict *problem.Problem, err error)
	UpdateDepositStatus(ctx context.Context, txid, status, e2eID string) error
	UpdateDepositPayer(ctx context.Context, txid, payerCPF, payerName string) error
	PutWithdrawal(ctx context.Context, w *wallet.Withdrawal) error
	GetWithdrawal(ctx context.Context, withdrawalID string) (*wallet.Withdrawal, error)
	UpdateWithdrawal(ctx context.Context, withdrawalID string, updates map[string]any) error
	ListProcessingWithdrawals(ctx context.Context, limit int) ([]wallet.Withdrawal, error)
	ListPendingDepositsOlderThan(ctx context.Context, cutoff time.Time, limit int) ([]wallet.PixDeposit, error)
	CreateHold(ctx context.Context, holdID, walletID, userID string, amount int64, tableRef, idemKey, reqHash string) (*wallet.Hold, bool, error)
	GetHold(ctx context.Context, holdID string) (*wallet.Hold, error)
	UpdateHoldStatus(ctx context.Context, holdID, fromStatus, toStatus string) (bool, error)
	ScanStaleHolds(ctx context.Context, cutoff time.Time, limit int) ([]wallet.Hold, error)
}

// Locker is the per-wallet lock surface.
type Locker interface {
	Acquire(ctx context.Context, walletID string) (func(), bool, error)
	AcquireOrdered(ctx context.Context, walletIDs ...string) (func(), bool, error)
}

// KYCClient is the account KYC surface.
type KYCClient interface {
	Get(ctx context.Context, userID string) (*kycclient.KYC, error)
}

// Auditor is the append-only audit surface for actions that move no money.
type Auditor interface {
	Append(ctx context.Context, e *wallet.AuditEvent) error
}

// Broadcaster pushes a real-time event to every WebSocket connection for a
// user. Optional — nil in cmd/reconcile and in unit tests, where no user is
// ever connected to receive it.
type Broadcaster interface {
	Broadcast(ctx context.Context, userID string, payload []byte)
}

// WalletService implements the wallet business flows.
type WalletService struct {
	repo        Repo
	users       UserRepo
	audit       Auditor
	lock        Locker
	pix         pix.PixClient
	kyc         KYCClient
	broadcaster Broadcaster // optional; see SetBroadcaster
}

func NewWalletService(repo Repo, users UserRepo, audit Auditor, lock Locker, pixClient pix.PixClient, kyc KYCClient) *WalletService {
	return &WalletService{repo: repo, users: users, audit: audit, lock: lock, pix: pixClient, kyc: kyc}
}

// SetBroadcaster wires the WebSocket registry after construction — kept as a
// setter rather than a constructor parameter so cmd/reconcile and every
// existing unit test's NewWalletService(...) call stays unchanged; a nil
// broadcaster makes ConfirmDeposit's broadcast a no-op.
func (s *WalletService) SetBroadcaster(b Broadcaster) {
	s.broadcaster = b
}

// ActivateGambling opens the caller's game + sandbox wallets. Gates: KYC
// `verified` (real money is about to enter a gambling ring-fence) and acceptance
// of the CURRENT gambling addendum — a separate document from the wallet terms.
//
// Idempotent: activating twice returns the same wallets. Writes an audit event,
// because consent must be provable after the fact.
func (s *WalletService) ActivateGambling(ctx context.Context, userID, kycLevel, ip, userAgent string, daily, weekly, monthly int64) (game, sandbox *wallet.Wallet, err error) {
	if kycLevel != wallet.KYCVerified {
		return nil, nil, problem.KYCNotVerified()
	}
	u, err := s.requireNotExcluded(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	if !u.GamblingAccepted() {
		return nil, nil, problem.GamblingTermsRequired()
	}
	// Personal limits are mandatory from day one: a user must never reach a
	// gambling wallet with no limits configured (router.go's own invariant).
	// An already-configured replay may omit them (zeros); anyone else sets
	// them here, which is the immediate first-set path of SetGameLimits.
	if !u.LimitsConfigured() {
		if _, err := s.SetGameLimits(ctx, userID, daily, weekly, monthly, ip, userAgent); err != nil {
			return nil, nil, err
		}
	}
	if _, err := s.repo.EnsureRealWallet(ctx, userID); err != nil {
		return nil, nil, err
	}

	// Already activated → return the existing wallets and append nothing. A replay
	// must not forge a second activation record: the audit log is evidence of what
	// actually happened, and one activation happened.
	if _, game, sandbox, err := s.repo.LoadWallets(ctx, userID); err != nil {
		return nil, nil, err
	} else if game != nil && sandbox != nil {
		return game, sandbox, nil
	}

	game, sandbox, err = s.repo.EnsureGamblingWallets(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	if err := s.audit.Append(ctx, &wallet.AuditEvent{
		UserID:    userID,
		EventType: wallet.EventGamblingActivated,
		Actor:     userID,
		After:     wallet.CurrentGamblingAddendumVersion,
		IP:        ip,
		UserAgent: userAgent,
	}); err != nil {
		return nil, nil, err
	}
	return game, sandbox, nil
}

// GetBalances returns the caller's wallets. The real wallet is created on first
// access; game and sandbox are nil until the user activates gambling, and their
// absence is what the frontend reads to decide whether to show a gambling surface
// at all.
func (s *WalletService) GetBalances(ctx context.Context, userID string) (real, game, sandbox *wallet.Wallet, err error) {
	if _, err := s.repo.EnsureRealWallet(ctx, userID); err != nil {
		return nil, nil, nil, err
	}
	return s.repo.LoadWallets(ctx, userID)
}

// Statement returns a paginated ledger for a wallet (newest first).
func (s *WalletService) Statement(ctx context.Context, walletID string, limit int, startKey map[string]types.AttributeValue) (*repositories.QueryResult, error) {
	return s.repo.Statement(ctx, walletID, limit, startKey)
}

// InitiateDeposit opens a PIX charge and records a pending deposit. Gates:
// kycLevel != "" (any verification started) and the amount within the wallet's
// deposit range. Not a balance mutation — money is credited only at
// ConfirmDeposit after re-querying the charge. idemKey is required for
// idempotency: a retried POST /wallet/deposits returns the same txid/QR and
// never opens a second Inter charge (SEC-08).
func (s *WalletService) InitiateDeposit(ctx context.Context, userID, kycLevel string, amount int64, idemKey string) (*wallet.PixDeposit, *pix.Charge, error) {
	if kycLevel == "" {
		return nil, nil, problem.KYCNotVerified()
	}
	realw, err := s.repo.EnsureRealWallet(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	// Absolute inbound ceiling — no per-wallet deposit-range override may exceed
	// it. Reject above-cap amounts before any charge is opened at Inter.
	if amount > wallet.MaxInboundAmount {
		return nil, nil, problem.AmountAboveLimit(wallet.MaxInboundAmount)
	}
	// Check the range before opening a charge at Inter — never create a PIX
	// charge for an amount we are going to reject.
	if err := wallet.ValidateDepositAmount(amount, realw); err != nil {
		minAmt, maxAmt := wallet.DepositLimits(realw)
		return nil, nil, problem.DepositOutOfRange(minAmt, maxAmt)
	}

	// SEC-08: register the idempotency key BEFORE opening any Inter charge so a
	// retried POST never opens a second PIX charge. On replay we return the prior
	// deposit + re-query its charge (idempotent).
	rh := reqHash("deposit#"+userID+"#"+idemKey, amount)
	guardPK := wallet.IdemPrefix + "initdep#" + idemKey
	txid, existing, conflict, err := s.repo.ReserveDepositIdem(ctx, guardPK, id.New(), userID, rh)
	if err != nil {
		return nil, nil, err
	}
	if conflict != nil {
		return nil, nil, conflict
	}
	if existing != nil {
		// Idempotent replay: return the original deposit + its (re-queried) charge.
		charge, qerr := s.pix.QueryCharge(ctx, txid)
		if qerr != nil {
			return nil, nil, qerr
		}
		return existing, charge, nil
	}

	charge, err := s.pix.CreateCharge(ctx, txid, amount, "")
	if err != nil {
		return nil, nil, problem.InternalServer("falha ao criar cobrança PIX: " + err.Error())
	}
	dep := &wallet.PixDeposit{
		Txid:           txid,
		WalletID:       realw.WalletID,
		UserID:         userID,
		AmountExpected: amount,
		Status:         wallet.DepositPending,
		CreatedAt:      repositories.NowStr(),
		TTL:            time.Now().Add(depositTTLMinutes * time.Minute).Unix(),
	}
	if err := s.repo.PutDeposit(ctx, dep); err != nil {
		return nil, nil, err
	}
	return dep, charge, nil
}

// ConfirmDeposit is invoked (indirectly) by the Inter webhook. It NEVER trusts
// the webhook payload for money movement: it re-queries the charge by txid and
// credits only when the charge is paid AND the payer CPF matches the user's KYC
// CPF. A mismatch is refunded automatically. Inter's charge re-query does NOT
// return the payer CPF/name (only the webhook does), so payerCPF/payerName are
// passed in from the webhook call and persisted on the deposit on first sight —
// payerCPF may be partially masked by Inter (e.g. "***137303**"), so the match
// below compares only the digits Inter actually reveals.
//
// A devolução (PIX refund) reported on re-query is handled too: if it lands
// before this deposit is confirmed, the deposit never credits; if it lands
// after, the credit is reversed (Invariant 12 — no money left in limbo).
// ConfirmDeposit re-queries the charge by txid (never the webhook body — Invariant
// #11) and credits it if paid. sweep=true is the reconciliation path: deposits
// whose webhook never arrived have no persisted payer CPF, and the re-query already
// proves the payment is for our txid, so the CPF anti-fraud gate is skipped and the
// deposit is credited rather than refunded (SEC-03). On the webhook path (sweep=false)
// a payer CPF is always present and must match KYC.
func (s *WalletService) ConfirmDeposit(ctx context.Context, txid, payerCPF, payerName string, sweep bool) error {
	dep, err := s.repo.GetDeposit(ctx, txid)
	if err != nil {
		return err
	}
	if dep == nil {
		return nil // unknown — idempotent no-op
	}

	if payerCPF != "" && payerCPF != dep.PayerCPF {
		if err := s.repo.UpdateDepositPayer(ctx, txid, payerCPF, payerName); err != nil {
			return err
		}
		dep.PayerCPF, dep.PayerName = payerCPF, payerName
	}

	charge, err := s.pix.QueryCharge(ctx, txid)
	if err != nil {
		return err
	}

	// A QR code can be scanned and paid by two different people at once — Inter
	// reports every payment received against the same txid. Only the first is
	// ever credited; everything else is refunded straight back to its payer,
	// regardless of this deposit's own status.
	if err := s.refundExcessPayments(ctx, dep, charge); err != nil {
		return err
	}

	if dep.Status == wallet.DepositConfirmed {
		// Already credited — a devolução here means the money left the PJ
		// account after the fact, so the credit must be reversed.
		return s.processDepositRefund(ctx, dep, charge)
	}
	if dep.Status != wallet.DepositPending {
		return nil // already resolved (rejected/refunded) — idempotent no-op
	}

	if charge.Status != pix.ChargeCompleted {
		return nil // not paid yet — safe to be re-woken later
	}

	if refunded(charge) {
		// Already returned to the payer before we got to confirm it — never credit.
		return s.repo.UpdateDepositStatus(ctx, txid, wallet.DepositRefunded, charge.E2EID)
	}

	kyc, err := s.kyc.Get(ctx, dep.UserID)
	if err != nil {
		return err
	}

	// CPF anti-fraud gate. On the webhook-driven path a payer CPF is always
	// present (persisted from the webhook body) and must match KYC. But the sweep
	// path re-confirms deposits whose webhook never arrived, so dep.PayerCPF is
	// empty — the charge re-query already proves the payment is for OUR txid, so
	// we credit it rather than refunding a genuinely paid deposit (SEC-03).
	if !(sweep && dep.PayerCPF == "") {
		if dep.PayerCPF == "" || !maskedCPFMatches(dep.PayerCPF, kyc.CPF) {
			return s.rejectMismatch(ctx, dep, charge)
		}
	}

	// Invariant 11 follow-through: the credited amount must match what we opened
	// the charge for. Inter caps a charge at its created amount, so a divergence
	// is anomalous — surface it as an alarm and refund rather than silently
	// crediting an unexpected value.
	if charge.Amount != dep.AmountExpected {
		slog.Error("ALARM deposit amount mismatch", "txid", txid, "expected", dep.AmountExpected, "paid", charge.Amount)
		return s.rejectMismatch(ctx, dep, charge)
	}

	release, ok, err := s.lock.Acquire(ctx, dep.WalletID)
	if err != nil {
		return err
	}
	if !ok {
		return problem.WalletBusy()
	}
	defer release()

	if _, _, err := s.repo.Credit(ctx, repositories.Mutation{
		WalletID:       dep.WalletID,
		Amount:         charge.Amount,
		EntryType:      wallet.EntryDeposit,
		Ref:            txid,
		IdempotencyKey: "deposit#" + txid,
		ReqHash:        reqHash(txid, charge.Amount),
	}); err != nil {
		return err
	}

	if err := s.repo.UpdateDepositStatus(ctx, txid, wallet.DepositConfirmed, charge.E2EID); err != nil {
		return err
	}
	s.broadcastDepositConfirmed(ctx, dep.UserID, dep.WalletID, txid, charge.Amount)
	return nil
}

// refunded reports whether the charge carries any completed devolução, per
// Inter's own re-query — never the webhook body (Invariant 11).
func refunded(charge *pix.Charge) bool {
	for _, r := range charge.Refunds {
		if r.Status == pix.RefundCompleted {
			return true
		}
	}
	return false
}

// maskedCPFMatches compares a possibly-masked CPF from Inter's webhook (e.g.
// "***137303**") against the full KYC CPF: every non-'*' digit must match at
// its position. A length mismatch or an all-masked value never matches — fail
// closed, matching the anti-fraud intent of the CPF gate.
func maskedCPFMatches(masked, full string) bool {
	if masked == "" || len(masked) != len(full) {
		return false
	}
	sawDigit := false
	for i := 0; i < len(masked); i++ {
		if masked[i] == '*' {
			continue
		}
		if masked[i] != full[i] {
			return false
		}
		sawDigit = true
	}
	return sawDigit
}

// refundExcessPayments returns straight to its payer every PIX received
// against this charge beyond the first — e.g. two people scanning and paying
// the same QR code at once. Only Payments[0] is ever credited (Amount stays
// the charge's nominal value, never the sum of payments), so this never
// touches the deposit's own status or the wallet balance; it only calls out to
// Inter. A refund failure is never silent (Invariant 12).
func (s *WalletService) refundExcessPayments(ctx context.Context, dep *wallet.PixDeposit, charge *pix.Charge) error {
	if len(charge.Payments) < 2 {
		return nil
	}
	for _, p := range charge.Payments[1:] {
		if refundedPayment(p) {
			continue // already returned
		}
		if _, err := s.pix.Refund(ctx, p.E2EID, p.Amount, "excess#"+p.E2EID); err != nil {
			slog.Error("ALARM excess PIX payment refund failed", "txid", dep.Txid, "e2e_id", p.E2EID, "amount", p.Amount, "err", err)
			return problem.InternalServer("estorno de pagamento excedente falhou; reconciliação manual necessária")
		}
	}
	return nil
}

func refundedPayment(p pix.Payment) bool {
	for _, r := range p.Refunds {
		if r.Status == pix.RefundCompleted {
			return true
		}
	}
	return false
}

// processDepositRefund reverses an already-credited deposit's ledger entry for
// every completed devolução Inter reports — the money left the PJ account, so
// the credit must be taken back rather than left standing.
func (s *WalletService) processDepositRefund(ctx context.Context, dep *wallet.PixDeposit, charge *pix.Charge) error {
	for _, r := range charge.Refunds {
		if r.Status != pix.RefundCompleted {
			continue
		}
		if err := s.reverseDeposit(ctx, dep, r); err != nil {
			return err
		}
	}
	return nil
}

// reverseDeposit debits the refunded amount back out of the wallet, keyed by
// the devolução's own rtrId so a retried webhook never double-debits. A debit
// failure (balance already spent) never fails silently: it flags the deposit
// for manual reconciliation and raises an alarm (Invariant 12).
func (s *WalletService) reverseDeposit(ctx context.Context, dep *wallet.PixDeposit, r pix.Refund) error {
	release, ok, err := s.lock.Acquire(ctx, dep.WalletID)
	if err != nil {
		return err
	}
	if !ok {
		return problem.WalletBusy()
	}
	defer release()

	idemKey := "deposit-refund#" + r.RtrID
	if _, _, err := s.repo.Debit(ctx, repositories.Mutation{
		WalletID:       dep.WalletID,
		Amount:         r.Amount,
		EntryType:      wallet.EntryDepositRefund,
		Ref:            dep.Txid,
		IdempotencyKey: idemKey,
		ReqHash:        reqHash(idemKey, r.Amount),
	}); err != nil {
		slog.Error("ALARM deposit refund debit failed", "txid", dep.Txid, "rtr_id", r.RtrID, "amount", r.Amount, "err", err)
		_ = s.repo.UpdateDepositStatus(ctx, dep.Txid, wallet.DepositRefundFailed, dep.E2EID)
		return problem.InternalServer("estorno de depósito falhou; reconciliação manual necessária")
	}
	return s.repo.UpdateDepositStatus(ctx, dep.Txid, wallet.DepositRefunded, dep.E2EID)
}

// broadcastDepositConfirmed pushes a real-time event to the user's connected
// WebSocket(s), if any (best-effort — a missed broadcast never blocks or fails
// the deposit; the ledger credit already committed). A nil broadcaster (e.g.
// cmd/reconcile, unit tests) is a silent no-op.
func (s *WalletService) broadcastDepositConfirmed(ctx context.Context, userID, walletID, txid string, amount int64) {
	if s.broadcaster == nil {
		return
	}
	payload, err := json.Marshal(map[string]any{
		"type":      "deposit_confirmed",
		"wallet_id": walletID,
		"txid":      txid,
		"amount":    amount,
	})
	if err != nil {
		slog.Error("broadcast deposit_confirmed: marshal failed", "user_id", userID, "err", err)
		return
	}
	s.broadcaster.Broadcast(ctx, userID, payload)
}

// broadcastWithdrawal pushes a real-time withdrawal-outcome event to the
// user's connected WebSocket(s), if any — same best-effort contract as
// broadcastDepositConfirmed. Shared by the synchronous Withdraw path and the
// async reconciliation job (reconcile.go), so both notify the same way.
func (s *WalletService) broadcastWithdrawal(ctx context.Context, userID, eventType, withdrawalID string, amount int64) {
	if s.broadcaster == nil {
		return
	}
	payload, err := json.Marshal(map[string]any{
		"type":          eventType,
		"withdrawal_id": withdrawalID,
		"amount":        amount,
	})
	if err != nil {
		slog.Error("broadcast "+eventType+": marshal failed", "user_id", userID, "err", err)
		return
	}
	s.broadcaster.Broadcast(ctx, userID, payload)
}

func (s *WalletService) rejectMismatch(ctx context.Context, dep *wallet.PixDeposit, charge *pix.Charge) error {
	if err := s.repo.UpdateDepositStatus(ctx, dep.Txid, wallet.DepositRejectedCPF, charge.E2EID); err != nil {
		return err
	}
	if _, err := s.pix.Refund(ctx, charge.E2EID, charge.Amount, "refund#"+dep.Txid); err != nil {
		// Refund failure leaves money in the PJ account with no owner — raise an
		// operational alarm for manual reconciliation (never a silent path).
		slog.Error("ALARM deposit refund failed", "txid", dep.Txid, "e2e_id", charge.E2EID, "amount", charge.Amount, "err", err)
		return problem.InternalServer("estorno do depósito falhou; reconciliação manual necessária")
	}
	return nil
}

// Withdraw debits amount+fee atomically then sends the PIX payout to the CPF
// on the caller's KYC record — the client never supplies a destination key
// (Invariant: PIX always goes to the registered owner, never an arbitrary
// key). Gates: verified KYC (also enforced at the handler). If the CPF has no
// PIX key registered at the bank, the debit is reversed immediately. Any
// other payout failure leaves the withdrawal in processing for the
// reconciliation job to resolve — money is never left in limbo.
func (s *WalletService) Withdraw(ctx context.Context, userID, kycLevel string, amount int64, idemKey string) (*wallet.Withdrawal, error) {
	if kycLevel != wallet.KYCVerified {
		return nil, problem.KYCNotVerified()
	}
	withdrawalID := "withdraw#" + userID + "#" + idemKey

	realw, err := s.repo.EnsureRealWallet(ctx, userID)
	if err != nil {
		return nil, err
	}

	release, ok, err := s.lock.Acquire(ctx, realw.WalletID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, problem.WalletBusy()
	}
	defer release()

	// Idempotent replay: same key → return the existing withdrawal. Checked
	// under the wallet lock (not before it) so two concurrent identical calls
	// can't both pass this check before either has written anything.
	if existing, err := s.repo.GetWithdrawal(ctx, withdrawalID); err != nil {
		return nil, err
	} else if existing != nil {
		return existing, nil
	}

	kyc, err := s.kyc.Get(ctx, userID)
	if err != nil {
		return nil, err
	}
	pixKey := kyc.CPF

	fee := wallet.WithdrawalFee(amount, realw)
	rh := reqHash(pixKey, amount)

	// Build the withdrawal record up front so the balance debit, both ledger
	// entries, the idempotency guard, AND the processing withdrawal row all
	// commit in a single TransactWriteItems. Previously the record was written
	// by a separate PutWithdrawal call, so a transient failure (or a crash)
	// between the two left a committed debit with no processing row — money in
	// limbo, unreconcilable (SEC-01, Invariant #12). Co-writing them makes the
	// debit and its tracking row atomic: on replay the guard AND the record both
	// exist, so GetWithdrawal returns it and there is no orphan (and no nil
	// deref in the handler).
	w := &wallet.Withdrawal{
		WithdrawalID:   withdrawalID,
		WalletID:       realw.WalletID,
		UserID:         userID,
		Amount:         amount,
		Fee:            fee,
		PixKey:         pixKey,
		Status:         wallet.WithdrawProcessing,
		IdempotencyKey: idemKey,
		CreatedAt:      repositories.NowStr(),
		UpdatedAt:      repositories.NowStr(),
	}
	_, _, replayed, err := s.repo.DebitWithFee(ctx, w, amount, fee, withdrawalID, rh)
	if err != nil {
		return nil, err
	}
	if replayed {
		// The debit itself was a replay (same idempotency key already
		// committed) — someone else is mid-flight on this withdrawal. Never
		// re-transfer; return whatever is on record.
		return s.repo.GetWithdrawal(ctx, withdrawalID)
	}

	res, err := s.pix.Transfer(ctx, pixKey, amount, interIdemKey(withdrawalID))
	if err != nil {
		if errors.Is(err, pix.ErrKeyNotFound) {
			// Nothing to retry — the registered CPF has no PIX key at the bank.
			// Refund now instead of leaving it processing for reconciliation.
			s.reverse(ctx, *w)
			return nil, problem.PixKeyNotFound()
		}
		// Debit already happened; leave processing for reconciliation to resolve.
		slog.Warn("withdrawal transfer failed, left in processing", "withdrawal_id", withdrawalID, "err", err)
		return w, nil
	}
	w.Status = wallet.WithdrawCompleted
	w.E2EID = res.E2EID
	if err := s.repo.UpdateWithdrawal(ctx, withdrawalID, map[string]any{"status": wallet.WithdrawCompleted, "e2e_id": res.E2EID}); err != nil {
		return nil, err
	}
	s.broadcastWithdrawal(ctx, userID, "withdraw_completed", withdrawalID, amount)
	return w, nil
}

// requireActivated loads the caller's wallets and fails if gambling was never
// activated. Every operation inside the ring-fence goes through this.
func (s *WalletService) requireActivated(ctx context.Context, userID string) (real, game, sandbox *wallet.Wallet, err error) {
	real, game, sandbox, err = s.repo.LoadWallets(ctx, userID)
	if err != nil {
		return nil, nil, nil, err
	}
	if real == nil || game == nil || sandbox == nil {
		return nil, nil, nil, problem.GamblingNotActivated()
	}
	return real, game, sandbox, nil
}

// ringTransfer moves money between two of the caller's wallets atomically,
// locking both. AcquireOrdered sorts the wallet IDs, so the lock order is total
// and deadlock-free for any number of wallets. The ledger pair and the
// idempotency guard are co-written in one transaction by repo.Transfer.
func (s *WalletService) ringTransfer(ctx context.Context, from, to *wallet.Wallet, amount, creditAmount int64, debitType, creditType, ns, idemKey string, extra ...types.TransactWriteItem) (debit, credit *wallet.LedgerEntry, err error) {
	release, ok, err := s.lock.AcquireOrdered(ctx, from.WalletID, to.WalletID)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, problem.WalletBusy()
	}
	defer release()

	key := ns + "#" + from.UserID + "#" + idemKey
	d, c, _, err := s.repo.Transfer(ctx, from.WalletID, to.WalletID, amount, creditAmount,
		debitType, creditType, key, key, reqHash(ns, amount), extra...)
	if err != nil {
		return nil, nil, err
	}
	return d, c, nil
}

// FundGame moves real money into the gambling ring-fence (real → game).
//
// This is the ONE edge by which real money reaches a game or sandbox, and the
// edge the personal limit engine meters. The limit is GROSS INFLOW: a later
// ReturnFromGame does NOT refund limit headroom, or a cap could be churned around
// indefinitely (fund → return → fund). The limit check itself belongs here, right
// before the transfer, and is added by the limit-engine plan.
func (s *WalletService) FundGame(ctx context.Context, userID string, amount int64, idemKey string) (debit, credit *wallet.LedgerEntry, err error) {
	// Absolute inbound ceiling — real money enters the gambling ring-fence only
	// here, so this is the single door the cap must guard.
	if amount > wallet.MaxInboundAmount {
		return nil, nil, problem.AmountAboveLimit(wallet.MaxInboundAmount)
	}
	rl, game, _, err := s.requireActivated(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	u, err := s.requireNotExcluded(ctx, userID)
	if err != nil {
		return nil, nil, err
	}

	// Personal limit engine: meter this deposit against the user's calendar
	// windows and co-write the bumped counters in the transfer's transaction.
	now := time.Now()
	lim, matured := u.EffectiveGameLimits(now)
	if matured { // lazy-apply a matured pending increase before metering
		if err := s.users.SetGameLimits(ctx, userID, new(lim)); err != nil {
			return nil, nil, err
		}
	}
	if !(u.LimitsConfigured() || matured) {
		return nil, nil, problem.LimitsNotConfigured()
	}
	var prev *wallet.GameDepositCounters
	var cur wallet.GameDepositCounters
	if u != nil && u.GameDepositCounters != nil {
		prev = u.GameDepositCounters
		cur = *prev
	}
	if breach := wallet.CheckDeposit(lim, cur, amount, now); breach != nil {
		return nil, nil, problem.DepositLimitExceeded(breach.Window, breach.Limit, breach.Used, breach.ResetsAt)
	}
	day, week, month := wallet.WindowKeys(now)
	d, w, m := cur.SumsFor(day, week, month)
	next := wallet.GameDepositCounters{
		DayKey: day, DaySum: d + amount,
		WeekKey: week, WeekSum: w + amount,
		MonthKey: month, MonthSum: m + amount,
	}
	counterTx, err := s.users.BumpDepositCounters(userID, prev, next)
	if err != nil {
		return nil, nil, err
	}
	return s.ringTransfer(ctx, rl, game, amount, amount,
		wallet.EntryGameFundDebit, wallet.EntryGameFundCredit, "game_fund", idemKey, counterTx)
}

// ReturnFromGame moves money back out of the ring-fence (game → real).
//
// Never limited and never charged a fee: moving money out of the ring-fence
// reduces the user's exposure, which is the behaviour the limits exist to
// encourage. This is not a PIX payout — to reach a bank account the user then
// withdraws from `real` as usual.
func (s *WalletService) ReturnFromGame(ctx context.Context, userID string, amount int64, idemKey string) (debit, credit *wallet.LedgerEntry, err error) {
	rl, game, _, err := s.requireActivated(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	return s.ringTransfer(ctx, game, rl, amount, amount,
		wallet.EntryGameReturnDebit, wallet.EntryGameReturnCredit, "game_return", idemKey)
}

// PurchaseSandbox converts game money into sandbox credits (game → sandbox).
//
// The source is the GAME wallet, never `real`: real money reaches sandbox only by
// first crossing the metered real → game edge. Were `real` spendable here, a user
// at their personal limit could simply buy sandbox directly and the limit would
// mean nothing. Sandbox remains a sink (Invariant #6) — this conversion is
// one-way and can never be undone.
func (s *WalletService) PurchaseSandbox(ctx context.Context, userID string, amount int64, idemKey string) (debit, credit *wallet.LedgerEntry, err error) {
	_, game, sandbox, err := s.requireActivated(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	// The debit is real money (centavos) from `game`; the credit is the same
	// amount converted into sandbox credits at the fixed rate. The two units are
	// different, so they are passed as separate amounts to ringTransfer.
	credits := wallet.ToSandboxCredits(amount)
	return s.ringTransfer(ctx, game, sandbox, amount, credits,
		wallet.EntrySandboxPurchase, wallet.EntrySandboxCredit, "sandbox_purchase", idemKey,
	)
}

// CreditSandbox grants sandbox currency to a user (M2M, e.g. poker/dominó bonus).
func (s *WalletService) CreditSandbox(ctx context.Context, userID string, amount int64, idemKey, reason string) (*wallet.LedgerEntry, error) {
	return s.sandboxOp(ctx, userID, amount, idemKey, reason, wallet.EntryGameCredit, true)
}

// DebitSandbox spends sandbox currency (M2M, e.g. a bet). Respects balance.
func (s *WalletService) DebitSandbox(ctx context.Context, userID string, amount int64, idemKey, reason string) (*wallet.LedgerEntry, error) {
	return s.sandboxOp(ctx, userID, amount, idemKey, reason, wallet.EntryGameDebit, false)
}

func (s *WalletService) sandboxOp(ctx context.Context, userID string, amount int64, idemKey, reason, entryType string, credit bool) (*wallet.LedgerEntry, error) {
	sandbox, err := s.repo.EnsureSandboxWallet(ctx, userID)
	if err != nil {
		return nil, err
	}
	release, ok, err := s.lock.Acquire(ctx, sandbox.WalletID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, problem.WalletBusy()
	}
	defer release()

	m := repositories.Mutation{
		WalletID:       sandbox.WalletID,
		Amount:         amount,
		EntryType:      entryType,
		Ref:            reason,
		IdempotencyKey: entryType + "#" + userID + "#" + idemKey,
		ReqHash:        reqHash(reason, amount),
	}
	var entry *wallet.LedgerEntry
	if credit {
		entry, _, err = s.repo.Credit(ctx, m)
	} else {
		entry, _, err = s.repo.Debit(ctx, m)
	}
	return entry, err
}

// DebitReal debits the real wallet for an authorized M2M client (e.g.
// ctech-billing charging a subscription). No PIX leg — this only moves money
// within the ledger, same shape as DebitSandbox but against `real`.
func (s *WalletService) DebitReal(ctx context.Context, userID string, amount int64, idemKey, reason string) (*wallet.LedgerEntry, error) {
	realw, err := s.repo.EnsureRealWallet(ctx, userID)
	if err != nil {
		return nil, err
	}
	release, ok, err := s.lock.Acquire(ctx, realw.WalletID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, problem.WalletBusy()
	}
	defer release()

	entry, _, err := s.repo.Debit(ctx, repositories.Mutation{
		WalletID:       realw.WalletID,
		Amount:         amount,
		EntryType:      wallet.EntryBillingDebit,
		Ref:            reason,
		IdempotencyKey: wallet.EntryBillingDebit + "#" + userID + "#" + idemKey,
		ReqHash:        reqHash(reason, amount),
	})
	return entry, err
}

// HoldGame reserves amount out of the caller's game wallet at buy-in — a real
// conditional debit (Invariant #1), not a soft reservation: GetBalances and the
// ledger continue to reflect the true spendable amount with no separate
// available-vs-held computation anywhere else. The resulting Hold record never
// bounds the eventual cash-out (see CashoutGame) — it exists for idempotency,
// audit, and stale-hold detection (see the reconciliation sweep in
// reconcile.go).
func (s *WalletService) HoldGame(ctx context.Context, userID string, amount int64, tableRef, idemKey string) (*wallet.Hold, error) {
	_, game, _, err := s.requireActivated(ctx, userID)
	if err != nil {
		return nil, err
	}
	if _, err := s.requireNotExcluded(ctx, userID); err != nil { // defense in depth: an excluded user must not re-enter play
		return nil, err
	}
	release, ok, err := s.lock.Acquire(ctx, game.WalletID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, problem.WalletBusy()
	}
	defer release()

	holdID := "hold#" + userID + "#" + idemKey
	h, _, err := s.repo.CreateHold(ctx, holdID, game.WalletID, userID, amount, tableRef,
		wallet.EntryGameHoldDebit+"#"+userID+"#"+idemKey, reqHash(tableRef, amount))
	return h, err
}

// ReleaseHold refunds a hold's full original amount — the plain-refund path for
// a table/hand that never played (e.g. the player leaves before any hand
// starts). Only valid on a `held` hold; an already-released/settled hold is a
// benign idempotent replay, not an error, so a caller retry never
// double-credits.
func (s *WalletService) ReleaseHold(ctx context.Context, userID, holdID, idemKey string) (*wallet.Hold, error) {
	h, err := s.repo.GetHold(ctx, holdID)
	if err != nil {
		return nil, err
	}
	if h == nil {
		return nil, problem.NotFound("hold não encontrado")
	}
	// SEC-07: a hold id is opaque but not proof of ownership. A compromised or
	// buggy internal client (scope internal:wallet:game-hold) must not be able to
	// release another user's hold. The route now requires the caller to name the
	// user; verify it matches before mutating.
	if h.UserID != userID {
		return nil, problem.Forbidden("hold não pertence ao usuário")
	}
	if h.Status != wallet.HoldHeld {
		return h, nil // already resolved — idempotent no-op
	}

	release, ok, err := s.lock.Acquire(ctx, h.WalletID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, problem.WalletBusy()
	}
	defer release()

	// Re-check under the lock: a concurrent release/cashout may have won the
	// race between the check above and acquiring the lock.
	h, err = s.repo.GetHold(ctx, holdID)
	if err != nil {
		return nil, err
	}
	if h.Status != wallet.HoldHeld {
		return h, nil
	}

	if _, _, err := s.repo.Credit(ctx, repositories.Mutation{
		WalletID:       h.WalletID,
		Amount:         h.Amount,
		EntryType:      wallet.EntryGameHoldRelease,
		Ref:            h.TableRef,
		IdempotencyKey: wallet.EntryGameHoldRelease + "#" + idemKey,
		ReqHash:        reqHash(holdID, h.Amount),
	}); err != nil {
		return nil, err
	}
	if ok, err := s.repo.UpdateHoldStatus(ctx, holdID, wallet.HoldHeld, wallet.HoldReleased); err != nil {
		return nil, err
	} else if !ok {
		// Lost a race to a concurrent transition after the credit above committed
		// (the credit is idempotent-keyed, so a retry of this call would simply
		// replay it) — return the current state rather than erroring.
		return s.repo.GetHold(ctx, holdID)
	}
	h.Status = wallet.HoldReleased
	return h, nil
}

// CashoutGame credits the caller's game wallet with amount — the calling
// skill game's own table ledger is authoritative for what a player's final
// stack is worth when they leave, so amount is credited exactly as sent,
// NEVER validated or bounded against the sum of the listed holds' amounts (see
// the design spec's "Why cash-out isn't bounded by the hold" — a player can
// leave with more than any single hold, e.g. having won another seated
// player's buy-in). Every listed hold is marked settled; a hold not currently
// `held` (already released/settled) is a benign idempotent-replay case, not an
// error, so a retry after a prior partial failure never fails the whole
// cash-out.
func (s *WalletService) CashoutGame(ctx context.Context, userID string, amount int64, tableRef string, holdIDs []string, idemKey string) (*wallet.LedgerEntry, error) {
	_, game, _, err := s.requireActivated(ctx, userID)
	if err != nil {
		return nil, err
	}
	release, ok, err := s.lock.Acquire(ctx, game.WalletID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, problem.WalletBusy()
	}
	defer release()

	// SEC-07: verify every listed hold belongs to this user before crediting or
	// settling. A compromised/bhuggy internal client (scope
	// internal:wallet:game-cashout) must not credit one user while settling
	// another's holds. Checked under the lock, before any mutation.
	for _, holdID := range holdIDs {
		hh, gerr := s.repo.GetHold(ctx, holdID)
		if gerr != nil {
			return nil, gerr
		}
		if hh == nil || hh.UserID != userID {
			return nil, problem.Forbidden("hold não pertence ao usuário")
		}
	}

	entry, _, err := s.repo.Credit(ctx, repositories.Mutation{
		WalletID:       game.WalletID,
		Amount:         amount,
		EntryType:      wallet.EntryGameCashoutCredit,
		Ref:            tableRef + "#" + strings.Join(holdIDs, ","),
		IdempotencyKey: wallet.EntryGameCashoutCredit + "#" + idemKey,
		ReqHash:        reqHash(tableRef, amount),
	})
	if err != nil {
		return nil, err
	}
	for _, holdID := range holdIDs {
		if _, err := s.repo.UpdateHoldStatus(ctx, holdID, wallet.HoldHeld, wallet.HoldSettled); err != nil {
			return nil, err
		}
	}
	return entry, nil
}

// reqHash is the canonical fingerprint guarding "same idempotency key, different
// payload" — the repository compares it and returns idempotency-conflict on drift.
func reqHash(ref string, amount int64) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s|%d", ref, amount)))
	return hex.EncodeToString(h[:])
}
