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

	"gopkg.aoctech.app/api-commons/observability"
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
const (
	depositTTLMinutes       = 60
	depositTxIDPrefix       = "dep"
	depositTxIDSeparator    = "\x00"
	withdrawalIDPrefix      = "withdraw#"
	withdrawalDescription   = "Saque via PIX"
	depositTxIDDigestLength = 30 // Inter txids are 26-35 alphanumeric chars: 3+30
	eventDepositConfirmed   = "deposit_confirmed"
	eventWithdrawalComplete = "withdraw_completed"
	eventWithdrawalFailed   = "withdraw_refund_failed"
	eventWithdrawalReversed = "withdraw_reversed"
)

// interWithdrawalNamespace namespaces the deterministic UUID sent to Inter as
// x-id-idempotente for PIX payouts (Inter rejects any other format). Derived
// via UUID v5 from withdrawalID, so it's stable across the initial Transfer
// call and every later reconciliation QueryTransfer for the same withdrawal.
// DO NOT EVER CHANGE
var interWithdrawalNamespace = uuid.MustParse("6f9c3b8e-6b0a-4b7e-9c1a-2f6f6e6f0a1a")

func interIdemKey(withdrawalID string) string {
	return uuid.NewSHA1(interWithdrawalNamespace, []byte(withdrawalID)).String()
}

// WalletStore owns wallet identity and balance reads.
type WalletStore interface {
	GetWallet(ctx context.Context, walletID string) (*wallet.Wallet, error)
	EnsureRealWallet(ctx context.Context, userID string) (*wallet.Wallet, error)
	EnsureSandboxWallet(ctx context.Context, userID string) (*wallet.Wallet, error)
	EnsureGamblingWallets(ctx context.Context, userID string) (game, sandbox *wallet.Wallet, err error)
	LoadWallets(ctx context.Context, userID string) (real, game, sandbox *wallet.Wallet, err error)
}

// LedgerStore owns atomic balance mutations and immutable ledger reads.
type LedgerStore interface {
	Credit(ctx context.Context, m repositories.Mutation, extra ...types.TransactWriteItem) (*wallet.LedgerEntry, bool, error)
	Debit(ctx context.Context, m repositories.Mutation, extra ...types.TransactWriteItem) (*wallet.LedgerEntry, bool, error)
	ConfirmDepositCredit(ctx context.Context, m repositories.Mutation, txid, e2eID string, extra ...types.TransactWriteItem) (*wallet.LedgerEntry, bool, error)
	FindMutation(ctx context.Context, idemKey, reqHash string) (*wallet.LedgerEntry, error)
	Transfer(ctx context.Context, from, to string, amount, creditAmount int64, debitType, creditType, ref, idemKey, reqHash string, extra ...types.TransactWriteItem) (*wallet.LedgerEntry, *wallet.LedgerEntry, bool, error)
	Statement(ctx context.Context, walletID string, limit int, startKey map[string]types.AttributeValue) (*repositories.QueryResult, error)
	AnyDebitSince(ctx context.Context, walletID, sinceSK string) (bool, error)
}

// DepositStore owns PIX deposit lifecycle persistence.
type DepositStore interface {
	PutDepositIfAbsent(ctx context.Context, d *wallet.PixDeposit) error
	GetDeposit(ctx context.Context, txid string) (*wallet.PixDeposit, error)
	UpdateDepositStatus(ctx context.Context, txid, status, e2eID string) error
	TransitionDepositStatus(ctx context.Context, txid, fromStatus, toStatus, e2eID string) (bool, error)
	UpdateDepositPayer(ctx context.Context, txid, payerCPF, payerName string) error
	ListPendingDepositsOlderThan(ctx context.Context, cutoff time.Time, limit int) ([]wallet.PixDeposit, error)
	ListRefundableDepositsOlderThan(ctx context.Context, cutoff time.Time, limit int) ([]wallet.PixDeposit, error)
}

// WithdrawalStore owns withdrawal state-machine persistence.
type WithdrawalStore interface {
	PutWithdrawal(ctx context.Context, w *wallet.Withdrawal) error
	WithdrawalPutTx(w *wallet.Withdrawal) (types.TransactWriteItem, error)
	MarkWithdrawalReversed(ctx context.Context, withdrawalID string, extra ...types.TransactWriteItem) error
	GetWithdrawal(ctx context.Context, withdrawalID string) (*wallet.Withdrawal, error)
	UpdateWithdrawal(ctx context.Context, withdrawalID string, updates map[string]any) error
	ListProcessingWithdrawals(ctx context.Context, limit int) ([]wallet.Withdrawal, error)
}

// HoldStore owns the game-funds reservation lifecycle.
type HoldStore interface {
	CreateHold(ctx context.Context, holdID, walletID, userID string, amount int64, tableRef, idemKey, reqHash string) (*wallet.Hold, bool, error)
	GetHold(ctx context.Context, holdID string) (*wallet.Hold, error)
	UpdateHoldStatus(ctx context.Context, holdID, fromStatus, toStatus string) (bool, error)
	ReleaseHoldAtomic(ctx context.Context, hold *wallet.Hold, idemKey, reqHash string) (*wallet.Hold, bool, error)
	CashoutHoldsAtomic(ctx context.Context, walletID, userID string, amount int64, tableRef string, holds []*wallet.Hold, idemKey, reqHash, description string) (*wallet.LedgerEntry, bool, error)
	ScanStaleHolds(ctx context.Context, cutoff time.Time, limit int) ([]wallet.Hold, error)
	ListOpenHoldsForWallet(ctx context.Context, walletID string, limit int) ([]wallet.Hold, error)
}

// Repo composes the persistence capabilities WalletService orchestrates. The
// smaller interfaces keep collaborators reusable by flows that need only one
// capability, while retaining the existing constructor contract.
type Repo interface {
	WalletStore
	LedgerStore
	DepositStore
	WithdrawalStore
	HoldStore
}

// Locker is the per-wallet lock surface.
type Locker interface {
	Acquire(ctx context.Context, walletID string) (func(), bool, error)
	AcquireOrdered(ctx context.Context, walletIDs ...string) (func(), bool, error)
}

func acquireWallet(ctx context.Context, locker Locker, walletID string) (func(), error) {
	release, acquired, err := locker.Acquire(ctx, walletID)
	if err != nil {
		return nil, err
	}
	if !acquired {
		return nil, problem.WalletBusy()
	}
	return release, nil
}

func acquireWallets(ctx context.Context, locker Locker, walletIDs ...string) (func(), error) {
	release, acquired, err := locker.AcquireOrdered(ctx, walletIDs...)
	if err != nil {
		return nil, err
	}
	if !acquired {
		return nil, problem.WalletBusy()
	}
	return release, nil
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

// SandboxPurchaseRepo is the persistence surface for the direct PIX→sandbox
// purchase flow (plan §9.1/§9.3) — its own repository, decoupled from Repo:
// a deposit is custody, this is a sale, and the two tables must never blur.
type SandboxPurchaseRepo interface {
	PutIfAbsent(ctx context.Context, p *wallet.SandboxPurchase) error
	Get(ctx context.Context, purchaseID string) (*wallet.SandboxPurchase, error)
	ListByUser(ctx context.Context, userID string, limit int, startKey map[string]types.AttributeValue) (*repositories.Page[wallet.SandboxPurchase], error)
	Update(ctx context.Context, purchaseID string, updates map[string]any) error
	BuildConfirmTx(purchaseID, e2eID, creditSK string) types.TransactWriteItem
	BuildRefundClaimTx(purchaseID string) types.TransactWriteItem
	TransitionStatus(ctx context.Context, purchaseID, fromStatus, toStatus string) (bool, error)
	ListPendingOlderThan(ctx context.Context, cutoff time.Time, limit int) ([]wallet.SandboxPurchase, error)
	ListRefundPendingOlderThan(ctx context.Context, cutoff time.Time, limit int) ([]wallet.SandboxPurchase, error)
	ListWebhookFailedOlderThan(ctx context.Context, cutoff time.Time, limit int) ([]wallet.SandboxPurchase, error)
}

// M2MClient is one registered M2M caller's notify-back configuration (e.g.
// ctech-poker) — loaded once at startup from a single SSM SecureString JSON
// blob (client_id → {webhook_url, hmac_secret}), the same "admin sets it,
// there is no API write path" posture as the wallets table's fee/deposit-range
// overrides. Keyed by the JWT's AZP claim, never client-supplied per-request:
// a caller-supplied callback URL would let any M2M token point the wallet's
// outbound call at an arbitrary host (SSRF), same reasoning as why the PIX
// deposit destination is always the caller's own KYC CPF, never a request body.
type M2MClient struct {
	WebhookURL string `json:"webhook_url"`
	HMACSecret string `json:"hmac_secret"`
	// MaxChargeCents caps a caller-supplied charge amount
	// (ScopeWalletChargeAmount). It is what replaces the catalogue as the fraud
	// defense for that route, so it lives here — in the same admin-set SSM blob,
	// with no API write path — rather than anywhere a request can reach.
	//
	// Absent means DefaultMaxChargeCents, never unlimited: a client added to the
	// blob without this field must not thereby be able to open a charge for any
	// amount at all.
	MaxChargeCents int64 `json:"max_charge_cents,omitempty"`
}

// DefaultMaxChargeCents is the ceiling for a client with none configured:
// R$ 1.000,00.
//
// A charge above it is refused, never truncated to it. A silently reduced charge
// produces a paid invoice that is still short, which is worse than a refusal in
// every way — the refusal is visible to the caller, and the short payment is
// discovered by an accountant.
const DefaultMaxChargeCents int64 = 100000

// MaxCharge is the effective ceiling for this client.
func (c M2MClient) MaxCharge() int64 {
	if c.MaxChargeCents <= 0 {
		return DefaultMaxChargeCents
	}
	return c.MaxChargeCents
}

// WalletService implements the wallet business flows.
type WalletService struct {
	repo             Repo
	users            UserRepo
	audit            Auditor
	lock             Locker
	pix              pix.PixClient
	kyc              KYCClient
	broadcaster      Broadcaster          // optional; see SetBroadcaster
	sandboxPurchases SandboxPurchaseRepo  // required for PurchaseSandboxDirect/RefundSandboxPurchase/ConfirmSandboxPurchase; see SetSandboxPurchases
	productPurchases ProductPurchaseRepo  // required for PurchaseProductDirect/ConfirmProductPurchase/RefundProductPurchase; see SetProductPurchases
	m2mClients       map[string]M2MClient // AZP → webhook config; nil/missing entry means "don't notify"; see SetM2MClients
}

func NewWalletService(repo Repo, users UserRepo, audit Auditor, lock Locker, pixClient pix.PixClient, kyc KYCClient) *WalletService {
	return &WalletService{
		repo: repo, users: users, audit: audit, lock: lock, pix: pixClient, kyc: kyc,
	}
}

// SetBroadcaster wires the WebSocket registry after construction — kept as a
// setter rather than a constructor parameter so cmd/reconcile and every
// existing unit test's NewWalletService(...) call stays unchanged; a nil
// broadcaster makes ConfirmDeposit's broadcast a no-op.
func (s *WalletService) SetBroadcaster(b Broadcaster) {
	s.broadcaster = b
}

// SetSandboxPurchases wires the direct-PIX sandbox-purchase repository (plan
// §9.1/§9.3) after construction — same setter pattern as SetBroadcaster,
// so every existing NewWalletService(...) call site keeps compiling
// unchanged. Unset, PurchaseSandboxDirect/RefundSandboxPurchase/
// ConfirmSandboxPurchase panic on first use — this feature ships live with no
// flag, so cmd/server and cmd/reconcile must always call this.
func (s *WalletService) SetSandboxPurchases(r SandboxPurchaseRepo) {
	s.sandboxPurchases = r
}

// SetM2MClients wires the registered M2M client → webhook-config map after
// construction — same setter pattern as SetSandboxPurchases. Unset (nil map),
// dispatchM2MWebhook finds no entry for any client and skips notification
// silently — correct for every deployment that has no M2M sandbox-purchase
// integration configured yet.
func (s *WalletService) SetM2MClients(m map[string]M2MClient) {
	s.m2mClients = m
}

// ActivateGambling opens the caller's game + sandbox wallets. Gates: KYC at
// least `basic` — any verification started — and acceptance of the CURRENT
// gambling addendum, a separate document from the wallet terms.
//
// The bar is a MINIMUM, matching the route's own RequireKYC(KYCBasic): an
// `enhanced` user has cleared strictly more than a `basic` one, so comparing
// for equality against `basic` refused exactly the users most entitled to
// activate. Depositing into the ring-fence is a separate, stricter gate
// (`real → game` still runs the limit engine), and money can only reach `game`
// from `real`, which is itself `enhanced`-only.
//
// Idempotent: activating twice returns the same wallets. Writes an audit event,
// because consent must be provable after the fact.
func (s *WalletService) ActivateGambling(ctx context.Context, userID, kycLevel, ip, userAgent string, daily, weekly, monthly int64) (game, sandbox *wallet.Wallet, err error) {
	if kycLevel == "" {
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
// access; game is nil until activation. sandbox may already exist independently
// and is returned so its read-only history remains accessible. Its presence is
// never evidence of gambling consent; callers derive activation from game only.

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

// depositTxID derives the Inter-compatible txid from the idempotency key, so a
// retried POST /wallet/deposits maps to the same charge. Mirrors
// sandboxPurchaseTxID: the digest keeps caller-controlled values out of the txid.
func depositTxID(userID, idemKey string) string {
	sum := sha256.Sum256([]byte(userID + depositTxIDSeparator + idemKey))
	return depositTxIDPrefix + hex.EncodeToString(sum[:])[:depositTxIDDigestLength]
}

// dailyDepositBreach reads the user's counters and reports whether depositing
// amount would overflow the wallet's daily cap. Advisory at initiation; the
// binding check is in ConfirmDeposit, under the wallet lock.
func (s *WalletService) dailyDepositBreach(ctx context.Context, userID string, realw *wallet.Wallet, amount int64) (*wallet.DailyBreach, error) {
	u, err := s.users.Get(ctx, userID)
	if err != nil {
		return nil, err
	}
	var c wallet.RealDailyCounters
	if u != nil && u.RealDailyCounters != nil {
		c = *u.RealDailyCounters
	}
	return wallet.CheckDailyDeposit(wallet.EffectiveDailyLimits(realw), c, amount, time.Now()), nil
}

// breachProblem maps a daily breach to its RFC 7807 problem (nil for no breach).
func breachProblem(b *wallet.DailyBreach) *problem.Problem {
	if b == nil {
		return nil
	}
	if b.Kind == wallet.DailyBreachDepositCap {
		return problem.DailyDepositLimit(b.Limit, b.Used, b.ResetsAt)
	}
	return problem.DailyWithdrawLimit(b.Kind, b.Limit, b.Used, b.ResetsAt)
}

// InitiateDeposit opens a PIX charge and records a pending deposit. Gates:
// kycLevel != "" (any verification started), the amount within the wallet's
// deposit range and, as an advisory pre-check, the daily deposit cap. Not a
// balance mutation: money is credited only at ConfirmDeposit after re-querying
// the charge. idemKey makes a retried POST return the same txid/QR and never
// open a second Inter charge (SEC-08).
func (s *WalletService) InitiateDeposit(ctx context.Context, userID, kycLevel string, amount int64, idemKey string) (*wallet.PixDeposit, *pix.Charge, error) {
	if kycLevel == "" {
		return nil, nil, problem.KYCNotVerified()
	}
	realw, err := s.repo.EnsureRealWallet(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	if amount > wallet.MaxInboundAmount {
		return nil, nil, problem.AmountAboveLimit(wallet.MaxInboundAmount)
	}
	// Range first: never open a PIX charge for an amount we will reject.
	if err := wallet.ValidateDepositAmount(amount, realw); err != nil {
		minAmt, maxAmt := wallet.DepositLimits(realw)
		return nil, nil, problem.DepositOutOfRange(minAmt, maxAmt)
	}

	txid := depositTxID(userID, idemKey)

	// Replay: the same key must mean the same request.
	existing, err := s.repo.GetDeposit(ctx, txid)
	if err != nil {
		return nil, nil, err
	}
	if existing != nil {
		if existing.UserID != userID || existing.AmountExpected != amount {
			return nil, nil, problem.IdempotencyConflict()
		}
		charge, qerr := s.pix.QueryCharge(ctx, txid)
		if qerr != nil {
			// Crash between the durable reservation and CreateCharge: Inter's
			// txid is unique, so re-creating with the same txid cannot open a
			// second charge.
			if charge, qerr = s.pix.CreateCharge(ctx, txid, existing.AmountExpected, ""); qerr != nil {
				return nil, nil, qerr
			}
		}
		return existing, charge, nil
	}

	// Advisory daily-cap pre-check for a NEW request. The binding enforcement is
	// at credit time; this only avoids opening a doomed charge.
	breach, err := s.dailyDepositBreach(ctx, userID, realw, amount)
	if err != nil {
		return nil, nil, err
	}
	if p := breachProblem(breach); p != nil {
		return nil, nil, p
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
	// Reserve the txid BEFORE opening the charge (SEC-08). Losing the race to a
	// concurrent identical request means it owns the charge: re-enter at the
	// replay branch (the row exists now, so this recurses at most once).
	if err := s.repo.PutDepositIfAbsent(ctx, dep); err != nil {
		if errors.Is(err, repositories.ErrDepositExists) {
			return s.InitiateDeposit(ctx, userID, kycLevel, amount, idemKey)
		}
		return nil, nil, err
	}
	charge, err := s.pix.CreateCharge(ctx, txid, amount, "")
	if err != nil {
		return nil, nil, problem.InternalServer("falha ao criar cobrança PIX: " + err.Error())
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

	charge, err := s.pix.QueryCharge(ctx, dep.Txid)
	if err != nil {
		return err
	}

	// A QR code can be scanned and paid by two different people at once — Inter
	// reports every payment received against the same txid. Only the first is
	// ever credited; everything else is refunded straight back to its payer,
	// regardless of this deposit's own status.
	if err := s.refundExcessPayments(ctx, dep.Txid, charge); err != nil {
		return err
	}

	switch dep.Status {
	case wallet.DepositConfirmed:
		// Already credited — a devolução here means the money left the PJ
		// account after the fact, so the credit must be reversed.
		return s.processDepositRefund(ctx, dep, charge)
	case wallet.DepositRefundPending, wallet.DepositRefundFailed, wallet.DepositRejectedCPF:
		// Resume a CPF/amount-mismatch compensation. DepositRejectedCPF is a
		// legacy state written before the provider refund by older releases.
		return s.refundMismatch(ctx, dep, charge)
	case wallet.DepositRefunded:
		// Repair the C-03 legacy window: an old release may have credited the
		// ledger, failed to mark confirmed, then observed the provider refund.
		prior, err := s.repo.FindMutation(ctx, "deposit#"+txid, reqHash(txid, charge.Amount))
		if err != nil {
			return err
		}
		if prior != nil {
			return s.processDepositRefund(ctx, dep, charge)
		}
		return nil
	case wallet.DepositPending:
		// Continue below.
	default:
		return nil
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

	// A provider re-query proves payment status and amount, but not ownership.
	// Never credit without payer identity evidence: doing so would let a third
	// party fund this account and the user withdraw the proceeds to their own CPF.
	// Some providers include the payer on re-query; persist that evidence when
	// available. Otherwise leave the deposit pending for webhook retry/manual
	// reconciliation instead of guessing or refunding an unidentified payment.
	if dep.PayerCPF == "" && charge.PayerCPF != "" {
		if err := s.repo.UpdateDepositPayer(ctx, txid, charge.PayerCPF, dep.PayerName); err != nil {
			return err
		}
		dep.PayerCPF = charge.PayerCPF
	}
	if dep.PayerCPF == "" {
		slog.Error("ALARM paid deposit missing payer identity; quarantined", "txid", txid, "sweep", sweep)
		return problem.InternalServer("depósito pago aguardando verificação do pagador")
	}
	if !maskedCPFMatches(dep.PayerCPF, kyc.CPF) {
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

	release, err := acquireWallet(ctx, s.lock, dep.WalletID)
	if err != nil {
		return err
	}
	defer release()

	// Daily cap, enforced HERE (not only at initiation): two charges opened in
	// parallel can each pass the advisory pre-check. Counters are read and bumped
	// under the real wallet lock, so this check cannot race itself.
	u, err := s.users.Get(ctx, dep.UserID)
	if err != nil {
		return err
	}
	realw, err := s.repo.GetWallet(ctx, dep.WalletID)
	if err != nil {
		return err
	}
	var prev *wallet.RealDailyCounters
	var cur wallet.RealDailyCounters
	if u != nil && u.RealDailyCounters != nil {
		prev = u.RealDailyCounters
		cur = *prev
	}
	now := time.Now()
	if breach := wallet.CheckDailyDeposit(wallet.EffectiveDailyLimits(realw), cur, charge.Amount, now); breach != nil {
		slog.Warn("deposit over daily cap; refunding payer", "txid", txid, "limit", breach.Limit, "used", breach.Used)
		return s.rejectMismatch(ctx, dep, charge)
	}
	day, _, _ := wallet.WindowKeys(now)
	next := cur.ForDay(day)
	next.DepositSum += charge.Amount
	counterTx, err := s.users.BumpRealDailyCounters(dep.UserID, prev, next)
	if err != nil {
		return err
	}

	if _, _, err := s.repo.ConfirmDepositCredit(ctx, repositories.Mutation{
		WalletID:       dep.WalletID,
		Amount:         charge.Amount,
		EntryType:      wallet.EntryDeposit,
		Ref:            txid,
		IdempotencyKey: "deposit#" + txid,
		ReqHash:        reqHash(txid, charge.Amount),
	}, txid, charge.E2EID, counterTx); err != nil {
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
func (s *WalletService) refundExcessPayments(ctx context.Context, txid string, charge *pix.Charge) error {
	if len(charge.Payments) < 2 {
		return nil
	}
	for _, p := range charge.Payments[1:] {
		if refundedPayment(p) {
			continue // already returned
		}
		if _, err := s.pix.Refund(ctx, p.E2EID, p.Amount, "excess#"+p.E2EID); err != nil {
			slog.Error("ALARM excess PIX payment refund failed", "txid", txid, "e2e_id", p.E2EID, "amount", p.Amount, "err", err)
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
	release, err := acquireWallet(ctx, s.lock, dep.WalletID)
	if err != nil {
		return err
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
		observability.Error(ctx, "ALARM deposit refund debit failed", err, "txid", dep.Txid, "rtr_id", r.RtrID, "amount", r.Amount)
		if updateErr := s.repo.UpdateDepositStatus(ctx, dep.Txid, wallet.DepositRefundFailed, dep.E2EID); updateErr != nil {
			observability.Error(ctx, "deposit refund failure status update failed", updateErr, "txid", dep.Txid)
		}
		return problem.InternalServer("estorno de depósito falhou; reconciliação manual necessária")
	}
	return s.repo.UpdateDepositStatus(ctx, dep.Txid, wallet.DepositRefunded, dep.E2EID)
}

// broadcastDepositConfirmed pushes a real-time event to the user's connected
// WebSocket(s), if any (best-effort — a missed broadcast never blocks or fails
// the deposit; the ledger credit already committed). A nil broadcaster (e.g.
// cmd/reconcile, unit tests) is a silent no-op.
func (s *WalletService) broadcastDepositConfirmed(ctx context.Context, userID, walletID, txid string, amount int64) {
	s.broadcastEvent(ctx, userID, eventDepositConfirmed, map[string]any{
		"type":      eventDepositConfirmed,
		"wallet_id": walletID,
		"txid":      txid,
		"amount":    amount,
	})
}

// broadcastWithdrawal pushes a real-time withdrawal-outcome event to the
// user's connected WebSocket(s), if any — same best-effort contract as
// broadcastDepositConfirmed. Shared by the synchronous Withdraw path and the
// async reconciliation job (reconcile.go), so both notify the same way.
func (s *WalletService) broadcastWithdrawal(ctx context.Context, userID, eventType, withdrawalID string, amount int64) {
	s.broadcastEvent(ctx, userID, eventType, map[string]any{
		"type":          eventType,
		"withdrawal_id": withdrawalID,
		"amount":        amount,
	})
}

func (s *WalletService) broadcastEvent(ctx context.Context, userID, eventType string, event any) {
	if s.broadcaster == nil {
		return
	}
	payload, err := json.Marshal(event)
	if err != nil {
		slog.Error("broadcast "+eventType+": marshal failed", "user_id", userID, "err", err)
		return
	}
	s.broadcaster.Broadcast(ctx, userID, payload)
}

func (s *WalletService) rejectMismatch(ctx context.Context, dep *wallet.PixDeposit, charge *pix.Charge) error {
	changed, err := s.repo.TransitionDepositStatus(ctx, dep.Txid, wallet.DepositPending, wallet.DepositRefundPending, charge.E2EID)
	if err != nil {
		return err
	}
	if !changed {
		current, err := s.repo.GetDeposit(ctx, dep.Txid)
		if err != nil {
			return err
		}
		if current == nil || current.Status == wallet.DepositRefunded {
			return nil
		}
		dep = current
	} else {
		dep.Status = wallet.DepositRefundPending
	}
	return s.refundMismatch(ctx, dep, charge)
}

// refundMismatch resumes the compensation from a durable non-terminal state.
// The provider key is stable, so a crash after the provider accepted the
// refund but before the local final transition is safe to replay.
func (s *WalletService) refundMismatch(ctx context.Context, dep *wallet.PixDeposit, charge *pix.Charge) error {
	if dep.Status != wallet.DepositRefundPending {
		changed, err := s.repo.TransitionDepositStatus(ctx, dep.Txid, dep.Status, wallet.DepositRefundPending, charge.E2EID)
		if err != nil {
			return err
		}
		if !changed {
			current, err := s.repo.GetDeposit(ctx, dep.Txid)
			if err != nil {
				return err
			}
			if current == nil || current.Status == wallet.DepositRefunded {
				return nil
			}
			if current.Status != wallet.DepositRefundPending {
				return problem.InternalServer("estado de estorno de depósito inconsistente")
			}
		}
	}
	// The provider is authoritative for whether the money already went back:
	// replaying an opaque timeout without observing the refund could create
	// another one.
	if refunded(charge) {
		return s.markDepositRefunded(ctx, dep)
	}

	_, refundErr := s.pix.Refund(ctx, charge.E2EID, charge.Amount, "refund#"+dep.Txid)
	if refundErr != nil {
		changed, stateErr := s.repo.TransitionDepositStatus(ctx, dep.Txid, wallet.DepositRefundPending, wallet.DepositRefundFailed, charge.E2EID)
		if stateErr != nil {
			slog.Error("ALARM deposit refund and durable failure transition both failed", "txid", dep.Txid, "refund_err", refundErr, "state_err", stateErr)
			return problem.InternalServer("estorno do depósito falhou; estado será reconciliado")
		}
		if !changed {
			current, readErr := s.repo.GetDeposit(ctx, dep.Txid)
			if readErr != nil {
				return readErr
			}
			// A concurrent worker may have completed the same stable provider
			// request. Never regress its terminal state to refund_failed.
			if current != nil && current.Status == wallet.DepositRefunded {
				return nil
			}
		}
		slog.Error("ALARM deposit refund failed; scheduled retry retained", "txid", dep.Txid, "e2e_id", charge.E2EID, "amount", charge.Amount, "err", refundErr)
		return problem.InternalServer("estorno do depósito falhou; nova tentativa agendada")
	}
	return s.markDepositRefunded(ctx, dep)
}

func (s *WalletService) markDepositRefunded(ctx context.Context, dep *wallet.PixDeposit) error {
	changed, err := s.repo.TransitionDepositStatus(ctx, dep.Txid, wallet.DepositRefundPending, wallet.DepositRefunded, dep.E2EID)
	if err != nil {
		return err
	}
	if !changed {
		current, err := s.repo.GetDeposit(ctx, dep.Txid)
		if err != nil {
			return err
		}
		if current == nil || current.Status != wallet.DepositRefunded {
			return problem.InternalServer("estorno concluído no provedor aguardando reconciliação local")
		}
	}
	return nil
}

// Withdraw debits exactly amount (no fee) and sends the PIX payout to the CPF on
// the caller's KYC record: the client never supplies a destination key, so a
// payout can only reach the registered owner. The debit, its ledger entry, the
// idempotency guard, the processing withdrawal row and the daily counters commit
// in ONE TransactWriteItems. If the CPF has no PIX key at the bank the debit is
// reversed immediately (and the daily slot returned); any other payout failure
// leaves the withdrawal processing for the reconciliation job (Invariant 14).
func (s *WalletService) Withdraw(ctx context.Context, userID, kycLevel string, amount int64, idemKey string) (*wallet.Withdrawal, error) {
	if kycLevel != wallet.KYCVerified {
		return nil, problem.KYCNotVerified()
	}
	withdrawalID := withdrawalIDPrefix + userID + "#" + idemKey

	realw, err := s.repo.EnsureRealWallet(ctx, userID)
	if err != nil {
		return nil, err
	}
	release, err := acquireWallet(ctx, s.lock, realw.WalletID)
	if err != nil {
		return nil, err
	}
	defer release()

	// Replay under the lock, so two concurrent identical calls cannot both pass.
	if existing, err := s.repo.GetWithdrawal(ctx, withdrawalID); err != nil {
		return nil, err
	} else if existing != nil {
		if existing.UserID != userID || existing.Amount != amount {
			return nil, problem.IdempotencyConflict()
		}
		return existing, nil
	}

	// Re-read under the lock: the wallet fetched before it can be stale, and both
	// the full-balance exemption and the per-wallet limits depend on it.
	if fresh, err := s.repo.GetWallet(ctx, realw.WalletID); err != nil {
		return nil, err
	} else if fresh != nil {
		realw = fresh
	}

	if err := wallet.ValidateWithdrawalAmount(amount, realw, amount == realw.Balance, false); err != nil {
		return nil, problem.BadRequest("valor abaixo do mínimo de saque")
	}

	u, err := s.users.Get(ctx, userID)
	if err != nil {
		return nil, err
	}
	var prev *wallet.RealDailyCounters
	var cur wallet.RealDailyCounters
	if u != nil && u.RealDailyCounters != nil {
		prev = u.RealDailyCounters
		cur = *prev
	}
	now := time.Now()
	if p := breachProblem(wallet.CheckDailyWithdraw(wallet.EffectiveDailyLimits(realw), cur, amount, now)); p != nil {
		return nil, p
	}
	day, _, _ := wallet.WindowKeys(now)
	next := cur.ForDay(day)
	next.WithdrawCount++
	next.WithdrawSum += amount
	counterTx, err := s.users.BumpRealDailyCounters(userID, prev, next)
	if err != nil {
		return nil, err
	}

	kyc, err := s.kyc.Get(ctx, userID)
	if err != nil {
		return nil, err
	}
	pixKey := kyc.CPF // destination is ALWAYS the KYC owner's CPF
	if pixKey == "" {
		return nil, problem.KYCNotVerified() // no CPF on record means no payout destination
	}

	w := &wallet.Withdrawal{
		WithdrawalID:   withdrawalID,
		WalletID:       realw.WalletID,
		UserID:         userID,
		Amount:         amount,
		PixKey:         pixKey,
		Status:         wallet.WithdrawProcessing,
		IdempotencyKey: idemKey,
		CreatedAt:      repositories.NowStr(),
		UpdatedAt:      repositories.NowStr(),
	}
	putTx, err := s.repo.WithdrawalPutTx(w)
	if err != nil {
		return nil, err
	}
	_, replayed, err := s.repo.Debit(ctx, repositories.Mutation{
		WalletID:       realw.WalletID,
		Amount:         amount,
		EntryType:      wallet.EntryWithdraw,
		Ref:            withdrawalID,
		Description:    withdrawalDescription,
		IdempotencyKey: withdrawalID,
		ReqHash:        reqHash(pixKey, amount),
	}, putTx, counterTx)
	if err != nil {
		return nil, err
	}
	if replayed {
		// Someone else is mid-flight on this withdrawal: never re-transfer.
		existing, err := s.repo.GetWithdrawal(ctx, withdrawalID)
		if err != nil {
			return nil, err
		}
		if existing == nil {
			return nil, problem.WalletBusy() // guard visible, row not yet: tell the client to retry
		}
		return existing, nil
	}

	res, err := s.pix.Transfer(ctx, pixKey, amount, interIdemKey(withdrawalID))
	if err != nil {
		if errors.Is(err, pix.ErrKeyNotFound) {
			// Nothing to retry: refund now instead of leaving it processing.
			s.reverse(ctx, *w)
			return nil, problem.PixKeyNotFound()
		}
		slog.Warn("withdrawal transfer failed, left in processing", "withdrawal_id", withdrawalID, "err", err)
		return w, nil
	}
	w.Status, w.E2EID = wallet.WithdrawCompleted, res.E2EID
	if err := s.repo.UpdateWithdrawal(ctx, withdrawalID, map[string]any{"status": wallet.WithdrawCompleted, "e2e_id": res.E2EID}); err != nil {
		return nil, err
	}
	s.broadcastWithdrawal(ctx, userID, eventWithdrawalComplete, withdrawalID, amount)
	return w, nil
}

// WalletBalances is the M2M balance snapshot a skill game reads to show a
// user how much they hold. real is deliberately excluded — poker never
// touches real money directly.
type WalletBalances struct {
	GameBalance    int64 `json:"game_balance"`
	SandboxBalance int64 `json:"sandbox_balance"`
}

// BalancesFor reports game+sandbox balances for a user. Read-only — it never
// creates a wallet; a wallet that doesn't exist yet reports as balance 0,
// which is the correct value (the user holds nothing there), not an error.
func (s *WalletService) BalancesFor(ctx context.Context, userID string) (*WalletBalances, error) {
	_, game, sandbox, err := s.repo.LoadWallets(ctx, userID)
	if err != nil {
		return nil, err
	}
	b := &WalletBalances{}
	if game != nil {
		b.GameBalance = game.Balance
	}
	if sandbox != nil {
		b.SandboxBalance = sandbox.Balance
	}
	return b, nil
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
	release, err := acquireWallets(ctx, s.lock, from.WalletID, to.WalletID)
	if err != nil {
		return nil, nil, err
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
	debit, credit, err = s.ringTransfer(ctx, game, sandbox, amount, credits,
		wallet.EntrySandboxPurchase, wallet.EntrySandboxCredit, "sandbox_purchase", idemKey,
	)
	if err != nil {
		return nil, nil, err
	}
	return debit, credit, nil
}

// CreditSandbox grants sandbox currency to a user (M2M, e.g. poker/dominó bonus).
func (s *WalletService) CreditSandbox(ctx context.Context, userID string, amount int64, idemKey, reason, description string) (*wallet.LedgerEntry, error) {
	return s.sandboxOp(ctx, userID, amount, idemKey, reason, description, wallet.EntryGameCredit, true)
}

// DebitSandbox spends sandbox currency (M2M, e.g. a bet). Respects balance.
func (s *WalletService) DebitSandbox(ctx context.Context, userID string, amount int64, idemKey, reason, description string) (*wallet.LedgerEntry, error) {
	return s.sandboxOp(ctx, userID, amount, idemKey, reason, description, wallet.EntryGameDebit, false)
}

func (s *WalletService) sandboxOp(ctx context.Context, userID string, amount int64, idemKey, reason, description, entryType string, credit bool) (*wallet.LedgerEntry, error) {
	sandbox, err := s.repo.EnsureSandboxWallet(ctx, userID)
	if err != nil {
		return nil, err
	}
	release, err := acquireWallet(ctx, s.lock, sandbox.WalletID)
	if err != nil {
		return nil, err
	}
	defer release()

	m := repositories.Mutation{
		WalletID:       sandbox.WalletID,
		Amount:         amount,
		EntryType:      entryType,
		Ref:            reason,
		Description:    description,
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
func (s *WalletService) DebitReal(ctx context.Context, userID string, amount int64, idemKey, reason, description string) (*wallet.LedgerEntry, error) {
	realw, err := s.repo.EnsureRealWallet(ctx, userID)
	if err != nil {
		return nil, err
	}
	release, err := acquireWallet(ctx, s.lock, realw.WalletID)
	if err != nil {
		return nil, err
	}
	defer release()

	entry, _, err := s.repo.Debit(ctx, repositories.Mutation{
		WalletID:       realw.WalletID,
		Amount:         amount,
		EntryType:      wallet.EntryBillingDebit,
		Ref:            reason,
		Description:    description,
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
	release, err := acquireWallet(ctx, s.lock, game.WalletID)
	if err != nil {
		return nil, err
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

	release, err := acquireWallet(ctx, s.lock, h.WalletID)
	if err != nil {
		return nil, err
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

	resolved, _, err := s.repo.ReleaseHoldAtomic(ctx, h,
		wallet.EntryGameHoldRelease+"#"+holdID, reqHash(holdID, h.Amount))
	return resolved, err
}

// CashoutGame atomically credits the caller's game wallet and consumes the
// listed holds. Until a table-wide, zero-sum settlement contract exists, the
// amount is fail-closed at the total value of the caller's held reservations;
// this prevents a compromised game client from minting wallet funds.
func (s *WalletService) CashoutGame(ctx context.Context, userID string, amount int64, tableRef string, holdIDs []string, idemKey, description string) (*wallet.LedgerEntry, error) {
	_, game, _, err := s.requireActivated(ctx, userID)
	if err != nil {
		return nil, err
	}
	release, err := acquireWallet(ctx, s.lock, game.WalletID)
	if err != nil {
		return nil, err
	}
	defer release()

	// SEC-07: verify every listed hold belongs to this user before crediting or
	// settling. A compromised/bhuggy internal client (scope
	// internal:wallet:game-cashout) must not credit one user while settling
	// another's holds. Checked under the lock, before any mutation.
	if len(holdIDs) > maxCashoutHolds {
		return nil, problem.BadRequest("quantidade de holds excede o limite")
	}
	seen := make(map[string]struct{}, len(holdIDs))
	holds := make([]*wallet.Hold, 0, len(holdIDs))
	var reserved int64
	for _, holdID := range holdIDs {
		if _, duplicate := seen[holdID]; duplicate {
			return nil, problem.BadRequest("hold duplicado")
		}
		seen[holdID] = struct{}{}
		hh, gerr := s.repo.GetHold(ctx, holdID)
		if gerr != nil {
			return nil, gerr
		}
		if hh == nil || hh.UserID != userID {
			return nil, problem.Forbidden("hold não pertence ao usuário")
		}
		if hh.Status != wallet.HoldHeld || hh.TableRef != tableRef {
			return nil, problem.Conflict("hold não está disponível para esta liquidação")
		}
		reserved += hh.Amount
		holds = append(holds, hh)
	}
	// Until the multi-player zero-sum settlement table is implemented, never
	// let a scoped caller mint value beyond the real funds these holds reserved.
	if amount > reserved {
		return nil, problem.BadRequest("cashout excede o valor reservado")
	}
	entry, _, err := s.repo.CashoutHoldsAtomic(ctx, game.WalletID, userID, amount, tableRef, holds,
		wallet.EntryGameCashoutCredit+"#"+userID+"#"+idemKey,
		reqHash(tableRef+"#"+strings.Join(holdIDs, ","), amount), description)
	return entry, err
}

const maxCashoutHolds = 20

// reqHash is the canonical fingerprint guarding "same idempotency key, different
// payload" — the repository compares it and returns idempotency-conflict on drift.
func reqHash(ref string, amount int64) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s|%d", ref, amount)))
	return hex.EncodeToString(h[:])
}
