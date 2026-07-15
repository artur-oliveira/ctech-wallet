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
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/artur-oliveira/ctech-wallet/api/internal/domain/id"
	"github.com/artur-oliveira/ctech-wallet/api/internal/domain/wallet"
	"github.com/artur-oliveira/ctech-wallet/api/internal/kycclient"
	"github.com/artur-oliveira/ctech-wallet/api/internal/pix"
	"github.com/artur-oliveira/ctech-wallet/api/internal/problem"
	"github.com/artur-oliveira/ctech-wallet/api/internal/repositories"
)

const depositTTLMinutes = 15

// Repo is the persistence surface the service depends on (dependency inversion
// so the service is unit-testable without DynamoDB).
type Repo interface {
	GetWallet(ctx context.Context, walletID string) (*wallet.Wallet, error)
	EnsureRealWallet(ctx context.Context, userID string) (*wallet.Wallet, error)
	EnsureGamblingWallets(ctx context.Context, userID string) (game, sandbox *wallet.Wallet, err error)
	LoadWallets(ctx context.Context, userID string) (real, game, sandbox *wallet.Wallet, err error)
	Credit(ctx context.Context, m repositories.Mutation) (*wallet.LedgerEntry, bool, error)
	Debit(ctx context.Context, m repositories.Mutation) (*wallet.LedgerEntry, bool, error)
	DebitWithFee(ctx context.Context, walletID string, amount, fee int64, idemKey, reqHash, ref string) (*wallet.LedgerEntry, *wallet.LedgerEntry, bool, error)
	Transfer(ctx context.Context, from, to string, amount int64, debitType, creditType, ref, idemKey, reqHash string) (*wallet.LedgerEntry, *wallet.LedgerEntry, bool, error)
	Statement(ctx context.Context, walletID string, limit int, startKey map[string]types.AttributeValue) (*repositories.QueryResult, error)
	PutDeposit(ctx context.Context, d *wallet.PixDeposit) error
	GetDeposit(ctx context.Context, txid string) (*wallet.PixDeposit, error)
	UpdateDepositStatus(ctx context.Context, txid, status, e2eID string) error
	PutWithdrawal(ctx context.Context, w *wallet.Withdrawal) error
	GetWithdrawal(ctx context.Context, withdrawalID string) (*wallet.Withdrawal, error)
	UpdateWithdrawal(ctx context.Context, withdrawalID string, updates map[string]any) error
	ListProcessingWithdrawals(ctx context.Context, limit int) ([]wallet.Withdrawal, error)
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
func (s *WalletService) ActivateGambling(ctx context.Context, userID, kycLevel, ip, userAgent string) (game, sandbox *wallet.Wallet, err error) {
	if kycLevel != wallet.KYCVerified {
		return nil, nil, problem.KYCNotVerified()
	}
	u, err := s.users.Get(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	if !u.GamblingAccepted() {
		return nil, nil, problem.GamblingTermsRequired()
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
// ConfirmDeposit after re-querying the charge.
func (s *WalletService) InitiateDeposit(ctx context.Context, userID, kycLevel string, amount int64) (*wallet.PixDeposit, *pix.Charge, error) {
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
	txid := id.New() // 26-char ULID — within Inter's txid charset/length
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
// the webhook payload: it re-queries the charge by txid and credits only when
// the charge is paid AND the payer CPF matches the user's KYC CPF. A mismatch is
// refunded automatically.
func (s *WalletService) ConfirmDeposit(ctx context.Context, txid string) error {
	dep, err := s.repo.GetDeposit(ctx, txid)
	if err != nil {
		return err
	}
	if dep == nil || dep.Status != wallet.DepositPending {
		return nil // unknown or already resolved — idempotent no-op
	}

	charge, err := s.pix.QueryCharge(ctx, txid)
	if err != nil {
		return err
	}
	if charge.Status != pix.ChargeCompleted {
		return nil // not paid yet — safe to be re-woken later
	}

	kyc, err := s.kyc.Get(ctx, dep.UserID)
	if err != nil {
		return err
	}

	if charge.PayerCPF == "" || charge.PayerCPF != kyc.CPF {
		return s.rejectMismatch(ctx, dep, charge)
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

// Withdraw debits amount+fee atomically then sends the PIX payout. Gates:
// verified KYC (also enforced at the handler) + DICT owner CPF == KYC CPF. If the
// payout call fails after the debit, the withdrawal stays in processing for the
// reconciliation job to resolve — money is never left in limbo.
func (s *WalletService) Withdraw(ctx context.Context, userID, kycLevel string, amount int64, pixKey, idemKey string) (*wallet.Withdrawal, error) {
	if kycLevel != wallet.KYCVerified {
		return nil, problem.KYCNotVerified()
	}
	withdrawalID := "withdraw#" + userID + "#" + idemKey

	// Idempotent replay: same key → return the existing withdrawal.
	if existing, err := s.repo.GetWithdrawal(ctx, withdrawalID); err != nil {
		return nil, err
	} else if existing != nil {
		return existing, nil
	}

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

	kyc, err := s.kyc.Get(ctx, userID)
	if err != nil {
		return nil, err
	}
	dict, err := s.pix.DictLookup(ctx, pixKey)
	if err != nil {
		// An unknown key is the user's typo, not our outage — 422, and never leak
		// the bank's response body back to the caller.
		if errors.Is(err, pix.ErrKeyNotFound) {
			return nil, problem.PixKeyNotFound()
		}
		slog.Error("DICT lookup failed", "err", err)
		return nil, problem.InternalServer("não foi possível consultar a chave PIX")
	}
	if dict.CPF == "" || dict.CPF != kyc.CPF {
		return nil, problem.WithdrawCPFMismatch()
	}

	fee := wallet.WithdrawalFee(amount, realw)
	rh := reqHash(pixKey, amount)
	if _, _, _, err := s.repo.DebitWithFee(ctx, realw.WalletID, amount, fee, withdrawalID, rh, withdrawalID); err != nil {
		return nil, err
	}

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
	if err := s.repo.PutWithdrawal(ctx, w); err != nil {
		return nil, err
	}

	res, err := s.pix.Transfer(ctx, pixKey, amount, withdrawalID)
	if err != nil {
		// Debit already happened; leave processing for reconciliation to resolve.
		slog.Warn("withdrawal transfer failed, left in processing", "withdrawal_id", withdrawalID, "err", err)
		return w, nil
	}
	w.Status = wallet.WithdrawCompleted
	w.E2EID = res.E2EID
	if err := s.repo.UpdateWithdrawal(ctx, withdrawalID, map[string]any{"status": wallet.WithdrawCompleted, "e2e_id": res.E2EID}); err != nil {
		return nil, err
	}
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
func (s *WalletService) ringTransfer(ctx context.Context, from, to *wallet.Wallet, amount int64, debitType, creditType, ns, idemKey string) (debit, credit *wallet.LedgerEntry, err error) {
	release, ok, err := s.lock.AcquireOrdered(ctx, from.WalletID, to.WalletID)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, problem.WalletBusy()
	}
	defer release()

	key := ns + "#" + from.UserID + "#" + idemKey
	d, c, _, err := s.repo.Transfer(ctx, from.WalletID, to.WalletID, amount,
		debitType, creditType, key, key, reqHash(ns, amount))
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
	real, game, _, err := s.requireActivated(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	return s.ringTransfer(ctx, real, game, amount,
		wallet.EntryGameFundDebit, wallet.EntryGameFundCredit, "game_fund", idemKey)
}

// ReturnFromGame moves money back out of the ring-fence (game → real).
//
// Never limited and never charged a fee: moving money out of the ring-fence
// reduces the user's exposure, which is the behaviour the limits exist to
// encourage. This is not a PIX payout — to reach a bank account the user then
// withdraws from `real` as usual.
func (s *WalletService) ReturnFromGame(ctx context.Context, userID string, amount int64, idemKey string) (debit, credit *wallet.LedgerEntry, err error) {
	real, game, _, err := s.requireActivated(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	return s.ringTransfer(ctx, game, real, amount,
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
	return s.ringTransfer(ctx, game, sandbox, amount,
		wallet.EntrySandboxPurchase, wallet.EntrySandboxCredit, "sandbox_purchase", idemKey)
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
	_, _, sandbox, err := s.requireActivated(ctx, userID)
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
		IdempotencyKey: entryType + "#" + idemKey,
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

// reqHash is the canonical fingerprint guarding "same idempotency key, different
// payload" — the repository compares it and returns idempotency-conflict on drift.
func reqHash(ref string, amount int64) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s|%d", ref, amount)))
	return hex.EncodeToString(h[:])
}
