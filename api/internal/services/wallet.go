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
const (
	depositTTLMinutes       = 60
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
	ConfirmDepositCredit(ctx context.Context, m repositories.Mutation, txid, e2eID string) (*wallet.LedgerEntry, bool, error)
	ApplyMedClawback(ctx context.Context, walletID, userID string, amount int64, ref, reqHash string) (debited, shortfall int64, replayed bool, err error)
	FindMutation(ctx context.Context, idemKey, reqHash string) (*wallet.LedgerEntry, error)
	DebitWithdrawal(ctx context.Context, w *wallet.Withdrawal, amount int64, idemKey, reqHash string) (*wallet.LedgerEntry, bool, error)
	Transfer(ctx context.Context, from, to string, amount, creditAmount int64, debitType, creditType, ref, idemKey, reqHash string, extra ...types.TransactWriteItem) (*wallet.LedgerEntry, *wallet.LedgerEntry, bool, error)
	Statement(ctx context.Context, walletID string, limit int, startKey map[string]types.AttributeValue) (*repositories.QueryResult, error)
	AnyDebitSince(ctx context.Context, walletID, sinceSK string) (bool, error)
	AmountAtSK(ctx context.Context, walletID, sk string) (int64, error)
}

// DepositStore owns PIX deposit lifecycle persistence.
type DepositStore interface {
	PutDeposit(ctx context.Context, d *wallet.PixDeposit) error
	GetDeposit(ctx context.Context, txid string) (*wallet.PixDeposit, error)
	GetDepositByProviderQRCodeID(ctx context.Context, providerQRCodeID string) (*wallet.PixDeposit, error)
	UpdateDepositProviderPaymentID(ctx context.Context, txid, providerPaymentID string) error
	ReserveDepositIdem(ctx context.Context, guardPK, txid, userID, reqHash string) (reservedTxid string, existing *wallet.PixDeposit, conflict *problem.Problem, err error)
	UpdateDepositStatus(ctx context.Context, txid, status, e2eID string) error
	TransitionDepositStatus(ctx context.Context, txid, fromStatus, toStatus, e2eID string) (bool, error)
	UpdateDepositPayer(ctx context.Context, txid, payerCPF, payerName string) error
	ListPendingDepositsOlderThan(ctx context.Context, cutoff time.Time, limit int) ([]wallet.PixDeposit, error)
	ListRefundableDepositsOlderThan(ctx context.Context, cutoff time.Time, limit int) ([]wallet.PixDeposit, error)
}

// WithdrawalStore owns withdrawal state-machine persistence.
type WithdrawalStore interface {
	PutWithdrawal(ctx context.Context, w *wallet.Withdrawal) error
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
	CashoutHoldsAtomic(ctx context.Context, walletID, userID string, amount int64, tableRef string, holds []*wallet.Hold, idemKey, reqHash string) (*wallet.LedgerEntry, bool, error)
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

// BaasProvider is the per-user Asaas custody gate WalletService reads before
// firing any conditional settlement leg (plan §4.1, §5.1, §9.1a). Defaults to
// a no-op that always reports "not custodied" (see noopBaasProvider) so every
// existing NewWalletService(...) call site — and every unit test — keeps
// working unchanged; wire the real one with SetBaas, same pattern as
// SetBroadcaster.
type BaasProvider interface {
	GetIfApproved(ctx context.Context, userID string) (*wallet.BaasAccount, error)
	// GetAccount returns the caller's BaasAccount regardless of status (nil if
	// absent) — GetBalances' onboarding-state branch (plan §4.1) needs the
	// in-progress states GetIfApproved deliberately collapses to nil.
	GetAccount(ctx context.Context, userID string) (*wallet.BaasAccount, error)
	// CreateDepositCharge opens a PIX QR code against the caller's own Asaas
	// subaccount (plan §4.2) — only ever called once InitiateDeposit has
	// already confirmed the subaccount is approved.
	CreateDepositCharge(ctx context.Context, userID string, amount int64, txid string) (charge *pix.Charge, providerQRCodeID string, err error)
	// QueryDepositPayment re-queries an Asaas-opened deposit by its provider QR
	// code ID, normalized into a pix.Charge so ConfirmDeposit's Invariant #11
	// re-query stays provider-agnostic (plan §4.3).
	QueryDepositPayment(ctx context.Context, userID, providerQRCodeID string) (*pix.Charge, error)
	// RefundDepositPayment returns an Asaas PIX payment to its original payer.
	// QueryDepositPayment is always called first and REFUNDED is the replay
	// observation, because Asaas allows more than one partial refund.
	RefundDepositPayment(ctx context.Context, userID, paymentID string, amount int64, reason string) error
	// SubmitWithdrawalPayout fires leg 1 of a custodied withdrawal (plan §5.2):
	// a PIX transfer from the user's own subaccount to their own registered CPF
	// key. Submission failure is non-fatal — the withdrawal stays `processing`
	// for cmd/reconcile, same contract as every other Asaas transfer leg.
	SubmitWithdrawalPayout(ctx context.Context, userID string, amount int64, pixKeyCPF, withdrawalID string) error
	// GetAccountByProviderID resolves an Asaas account.id (as reported by any
	// webhook) back to its BaasAccount row — nil if unknown (plan §7.1, §7.3).
	GetAccountByProviderID(ctx context.Context, providerAccountID string) (*wallet.BaasAccount, error)
	// PutMedReceivableIfAbsent records a MED clawback shortfall (plan §7.3),
	// idempotent by the receivable's own deterministic ID.
	PutMedReceivableIfAbsent(ctx context.Context, m *wallet.MedReceivable) error
	// HasOpenMedReceivable reports whether walletID has an outstanding MED
	// clawback debt — funding/withdrawal stay blocked while one is open (plan
	// §7.3 point 3), settled automatically from the next inflow.
	HasOpenMedReceivable(ctx context.Context, walletID string) (bool, error)
	// SetAccountStatus transitions a BaasAccount's lifecycle status directly —
	// used by the closure state machine (plan §7.2).
	SetAccountStatus(ctx context.Context, userID, status string) error
	// CountReceipt records one PIX receipt against the subaccount's monthly
	// free allowance, rolling the window when monthKey changes. Called after a
	// deposit is confirmed, because a receipt only costs money once it is
	// actually received — an unpaid QR code is billed nothing.
	CountReceipt(ctx context.Context, userID, monthKey string) error
	// SubmitGamePurchaseSettlement/SubmitGamePurchaseReversal are §9.1a's
	// forward/reverse settlement legs for a game-funded sandbox purchase.
	SubmitGamePurchaseSettlement(ctx context.Context, userID, creditSK string, amount int64) error
	SubmitGamePurchaseReversal(ctx context.Context, userID, creditSK string, amount int64) error
}

type noopBaasProvider struct{}

func (noopBaasProvider) GetIfApproved(context.Context, string) (*wallet.BaasAccount, error) {
	return nil, nil
}

func (noopBaasProvider) GetAccount(context.Context, string) (*wallet.BaasAccount, error) {
	return nil, nil
}

func (noopBaasProvider) CreateDepositCharge(context.Context, string, int64, string) (*pix.Charge, string, error) {
	return nil, "", errors.New("baas: custody disabled")
}

func (noopBaasProvider) QueryDepositPayment(context.Context, string, string) (*pix.Charge, error) {
	return nil, errors.New("baas: custody disabled")
}

func (noopBaasProvider) RefundDepositPayment(context.Context, string, string, int64, string) error {
	return errors.New("baas: custody disabled")
}

func (noopBaasProvider) SubmitWithdrawalPayout(context.Context, string, int64, string, string) error {
	return errors.New("baas: custody disabled")
}

func (noopBaasProvider) GetAccountByProviderID(context.Context, string) (*wallet.BaasAccount, error) {
	return nil, nil
}

func (noopBaasProvider) PutMedReceivableIfAbsent(context.Context, *wallet.MedReceivable) error {
	return errors.New("baas: custody disabled")
}

func (noopBaasProvider) HasOpenMedReceivable(context.Context, string) (bool, error) {
	return false, nil
}

func (noopBaasProvider) SetAccountStatus(context.Context, string, string) error {
	return errors.New("baas: custody disabled")
}

func (noopBaasProvider) CountReceipt(context.Context, string, string) error {
	return errors.New("baas: custody disabled")
}

func (noopBaasProvider) SubmitGamePurchaseSettlement(context.Context, string, string, int64) error {
	return errors.New("baas: custody disabled")
}

func (noopBaasProvider) SubmitGamePurchaseReversal(context.Context, string, string, int64) error {
	return errors.New("baas: custody disabled")
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
	baas             BaasProvider         // defaults to noopBaasProvider; see SetBaas
	receiptsPerMonth int64                // monthly PIX-receipt allowance per subaccount; see SetReceiptsPerMonth
	sandboxPurchases SandboxPurchaseRepo  // required for PurchaseSandboxDirect/RefundSandboxPurchase/ConfirmSandboxPurchase; see SetSandboxPurchases
	productPurchases ProductPurchaseRepo  // required for PurchaseProductDirect/ConfirmProductPurchase/RefundProductPurchase; see SetProductPurchases
	m2mClients       map[string]M2MClient // AZP → webhook config; nil/missing entry means "don't notify"; see SetM2MClients
}

func NewWalletService(repo Repo, users UserRepo, audit Auditor, lock Locker, pixClient pix.PixClient, kyc KYCClient) *WalletService {
	return &WalletService{
		repo: repo, users: users, audit: audit, lock: lock, pix: pixClient, kyc: kyc,
		baas:             noopBaasProvider{},
		receiptsPerMonth: wallet.DefaultReceiptsPerMonth,
	}
}

// SetReceiptsPerMonth wires config.AsaasFreeReceiptsPerMonth after
// construction — same setter pattern as SetBroadcaster/SetBaas, so existing
// call sites keep the safe default rather than an accidental zero (which would
// refuse every deposit).
func (s *WalletService) SetReceiptsPerMonth(v int64) {
	if v > 0 {
		s.receiptsPerMonth = v
	}
}

// SetBroadcaster wires the WebSocket registry after construction — kept as a
// setter rather than a constructor parameter so cmd/reconcile and every
// existing unit test's NewWalletService(...) call stays unchanged; a nil
// broadcaster makes ConfirmDeposit's broadcast a no-op.
func (s *WalletService) SetBroadcaster(b Broadcaster) {
	s.broadcaster = b
}

// SetBaas wires the real Asaas custody gate after construction — same setter
// pattern as SetBroadcaster, so every existing NewWalletService(...) call
// site keeps compiling unchanged. Without a call to SetBaas, s.baas stays the
// noopBaasProvider set by the constructor (never custodied), matching
// pre-migration behavior exactly.
func (s *WalletService) SetBaas(b BaasProvider) {
	s.baas = b
}

// CustodyEnabledForUser reports the internal real-wallet allowlist state. It
// is intentionally not a self-service switch or public response field.
//
// Since deposits became Asaas-only it gates ONBOARDING, not depositing: it
// answers "may this user open a subaccount", which is the expensive, scarce
// action (a non-refundable verification fee and a subaccount slot per attempt).
// Whether an already-onboarded user may deposit is decided by
// requireCustodyForDeposit, which never consults it.
func (s *WalletService) CustodyEnabledForUser(ctx context.Context, userID string) (bool, error) {
	real, err := s.repo.EnsureRealWallet(ctx, userID)
	if err != nil {
		return false, err
	}
	return real.CustodyEnabled, nil
}

// SetSandboxPurchases wires the direct-PIX sandbox-purchase repository (plan
// §9.1/§9.3) after construction — same setter pattern as SetBroadcaster/
// SetBaas, so every existing NewWalletService(...) call site keeps compiling
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

// ActivateGambling opens the caller's game + sandbox wallets. Gates: KYC
// `verified` (real money is about to enter a gambling ring-fence) and acceptance
// of the CURRENT gambling addendum — a separate document from the wallet terms.
//
// Idempotent: activating twice returns the same wallets. Writes an audit event,
// because consent must be provable after the fact.
func (s *WalletService) ActivateGambling(ctx context.Context, userID, kycLevel, ip, userAgent string, daily, weekly, monthly int64) (game, sandbox *wallet.Wallet, err error) {
	if kycLevel != wallet.KYCBasic {
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
//
// custodyStatus is only meaningful when AsaasCustodyEnabled (plan §4.1): if the
// caller's Asaas subaccount is absent or not yet approved, real/game/sandbox
// are all nil, custodyStatus carries the current onboarding lifecycle state
// ("onboarding", "pending_documents", ...), and EnsureRealWallet is
// deliberately never called — "a real wallet may not exist before a custody
// account exists to back it, otherwise Invariant #13 is false from birth"
// (plan §3.2). With the flag off (the default everywhere outside a dedicated
// Asaas-sandbox test environment), custodyStatus is always "" and behavior is
// byte-for-byte what it was before this branch existed.
func (s *WalletService) GetBalances(ctx context.Context, userID string) (real, game, sandbox *wallet.Wallet, custodyStatus string, err error) {
	if _, err := s.repo.EnsureRealWallet(ctx, userID); err != nil {
		return nil, nil, nil, "", err
	}
	real, game, sandbox, err = s.repo.LoadWallets(ctx, userID)
	if err != nil || real == nil {
		return real, game, sandbox, "", err
	}
	if real.CustodyEnabled {
		acc, err := s.baas.GetAccount(ctx, userID)
		if err != nil {
			return nil, nil, nil, "", err
		}
		if acc == nil {
			return nil, nil, nil, wallet.BaasOnboarding, nil
		}
		if acc.Status != wallet.BaasApproved {
			return nil, nil, nil, acc.Status, nil
		}
		custodyStatus = wallet.BaasApproved
	}
	return real, game, sandbox, custodyStatus, err
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
// requireCustodyApproved gates a money-OUT flow on the caller's Asaas
// subaccount being approved. A wallet outside the custody allowlist has no
// subaccount and returns (nil, nil): its balance, if any, is legacy Inter money
// and must stay withdrawable through Inter — refusing it would strand real
// money (Invariant #12). Money-IN has no such history to honour and uses
// requireCustodyForDeposit instead.
func (s *WalletService) requireCustodyApproved(ctx context.Context, userID string) (*wallet.BaasAccount, error) {
	real, err := s.repo.EnsureRealWallet(ctx, userID)
	if err != nil {
		return nil, err
	}
	if !real.CustodyEnabled {
		return nil, nil
	}
	return s.requireCustodyForDeposit(ctx, userID)
}

// requireCustodyForDeposit is the money-IN gate, and it never returns
// (nil, nil): a deposit ALWAYS needs an approved Asaas subaccount to land in
// (Invariant #14). It deliberately does not consult the real wallet's
// custody_enabled allowlist — that flag decides who may open a subaccount, and
// reading it here would re-open the Inter deposit path for everyone not on it,
// which is the exact hole this replaced.
//
// Returns (acc, nil) once approved; otherwise a 409 wallet-onboarding carrying
// the current lifecycle status so the UI can show the right onboarding step.
func (s *WalletService) requireCustodyForDeposit(ctx context.Context, userID string) (*wallet.BaasAccount, error) {
	acc, err := s.baas.GetIfApproved(ctx, userID)
	if err != nil {
		return nil, err
	}
	if acc != nil {
		if acc.ConservationDrift {
			// Invariant #13 fail-closed kill-switch (plan §6): this user's Asaas
			// balance and this ledger disagree — never act on either side until
			// ops reconciles the drift and clears the flag.
			return nil, problem.AccountBlocked()
		}
		return acc, nil
	}
	full, err := s.baas.GetAccount(ctx, userID)
	if err != nil {
		return nil, err
	}
	status := wallet.BaasOnboarding
	if full != nil {
		status = full.Status
	}
	if status == wallet.BaasFrozen {
		// Distinct problem type from every other non-approved status (plan
		// §7.1): frozen is not "still onboarding," it is a live block on an
		// already-approved account — the UI must never render an onboarding
		// step for it.
		return nil, problem.AccountBlocked()
	}
	return nil, problem.WalletOnboarding(status)
}

// requireReceiptAllowance refuses a new deposit charge once the caller's
// subaccount has used its monthly PIX-receipt allowance. Asaas gives every
// account a fixed number of free receipts per calendar month and bills each one
// after that, so without this gate a busy wallet quietly turns into a per-PIX
// cost with nothing capping it.
//
// ponytail: counts CONFIRMED receipts, not opened QR codes — an unpaid QR is
// billed nothing, so reserving a slot at open time would count the wrong thing.
// The configured allowance sits below the real free ceiling and that margin is
// what covers charges opened but not yet paid (short TTL, one at a time per
// wallet via the wallet lock). If per-user volume ever gets near the margin,
// this becomes a reservation released on expiry.
func (s *WalletService) requireReceiptAllowance(ctx context.Context, userID string) error {
	acc, err := s.baas.GetAccount(ctx, userID)
	if err != nil {
		return err
	}
	now := time.Now()
	_, _, month := wallet.WindowKeys(now)
	used := acc.ReceiptsUsed(month)
	if used < s.receiptsPerMonth {
		return nil
	}
	_, _, resetsAt := wallet.WindowResets(now)
	return problem.DepositReceiptsExhausted(s.receiptsPerMonth, resetsAt)
}

// Deposit gate reasons reported by DepositReadiness. They exist so the frontend
// can render the right next step BEFORE the user types an amount, instead of
// letting them submit into a 403/409 they could not have predicted. The client
// renders whatever BlockedBy says and never re-derives it from KYCLevel /
// CustodyStatus — one place decides, so UI and API cannot drift apart.
const (
	// DepositBlockedKYC: the caller's kyc_level is below the `enhanced` the
	// deposit route demands (and, when custody applies, the same level
	// POST /wallet/onboarding demands to open the subaccount).
	DepositBlockedKYC = "kyc"
	// DepositBlockedCustodyAbsent: no subaccount has been opened yet and the
	// user is allowed to open one — the one case the user resolves themselves.
	DepositBlockedCustodyAbsent = "custody_absent"
	// DepositBlockedCustodyFeePending: onboarding started and is waiting on the
	// one-off verification fee. The user acts on this by paying the charge.
	DepositBlockedCustodyFeePending = "custody_fee_pending"
	// DepositBlockedCustodyDocuments: Asaas is waiting on identity documents.
	// The user acts on this by following the provider's onboarding link.
	DepositBlockedCustodyDocuments = "custody_documents"
	// DepositBlockedCustodyPending: a subaccount exists but is not approved
	// yet. Nothing for the user to do but wait.
	DepositBlockedCustodyPending = "custody_pending"
	// DepositBlockedCustodyBlocked: frozen, closing/closed, or conservation
	// drift. Never an onboarding step — this needs support.
	DepositBlockedCustodyBlocked = "custody_blocked"
)

// DepositReadiness is the pre-flight answer to "can this user deposit right
// now, and if not, what is their next step?" — surfaced on GET /wallet/me so
// the dashboard already has it before the deposit button is rendered.
type DepositReadiness struct {
	Allowed         bool   `json:"allowed"`
	BlockedBy       string `json:"blocked_by,omitempty"`
	KYCLevel        string `json:"kyc_level"`
	CustodyRequired bool   `json:"custody_required"`
	CustodyStatus   string `json:"custody_status,omitempty"`
}

// DepositReadiness evaluates the exact gates InitiateDeposit enforces, through
// the very same requireCustodyApproved, and reports them as data instead of as
// a rejection. A read-only pre-flight: it opens nothing, charges nothing, and
// its answer is advisory — InitiateDeposit still enforces every gate itself.
//
// The KYC bar reported here is the ROUTE's, not this service method's: POST
// /wallet/deposits carries RequireKYC(KYCVerified), so a `basic` user is
// refused by the middleware before InitiateDeposit's own weaker `!= ""` check
// ever runs. Reporting "basic is enough" would send them into a 403.
func (s *WalletService) DepositReadiness(ctx context.Context, userID, kycLevel string) (*DepositReadiness, error) {
	// Custody is no longer conditional: there is exactly one deposit rail and it
	// is the user's own Asaas subaccount. The field stays in the response for
	// the frontend's benefit, always true.
	out := &DepositReadiness{KYCLevel: kycLevel, CustodyRequired: true}

	acc, err := s.baas.GetAccount(ctx, userID)
	if err != nil {
		return nil, err
	}
	if acc != nil {
		out.CustodyStatus = acc.Status
	}

	if kycLevel != wallet.KYCVerified {
		out.BlockedBy = DepositBlockedKYC
		return out, nil
	}

	if _, err := s.requireCustodyForDeposit(ctx, userID); err != nil {
		var p *problem.Problem
		if !errors.As(err, &p) {
			// A repository/provider failure is not a gate. Fail open here so a
			// transient read never tells a fully-onboarded user they cannot
			// deposit; InitiateDeposit remains the enforcing edge.
			return nil, err
		}
		if out.CustodyStatus == "" {
			// No subaccount yet. "Open one" is only the next step for a user the
			// allowlist actually lets open one; for anyone else there is no
			// self-service action, so saying so would be a dead end.
			allowed, aerr := s.CustodyEnabledForUser(ctx, userID)
			if aerr != nil {
				return nil, aerr
			}
			out.BlockedBy = DepositBlockedCustodyBlocked
			if allowed {
				out.BlockedBy = DepositBlockedCustodyAbsent
			}
			return out, nil
		}
		out.BlockedBy = custodyBlockReason(out.CustodyStatus)
		return out, nil
	}
	out.Allowed = true
	return out, nil
}

// custodyBlockReason maps a subaccount lifecycle status to the user-facing
// next step. Only called with a non-empty status — the no-row case needs the
// allowlist to decide and is handled by DepositReadiness itself.
func custodyBlockReason(status string) string {
	switch status {
	case wallet.BaasFeePending:
		return DepositBlockedCustodyFeePending
	case wallet.BaasPendingDocuments:
		return DepositBlockedCustodyDocuments
	case wallet.BaasFrozen, wallet.BaasClosing, wallet.BaasSubaccountClosed, wallet.BaasClosed,
		// requireCustodyForDeposit also refuses an APPROVED account under
		// conservation drift (Invariant #13). Approved-but-refused is never an
		// onboarding step.
		wallet.BaasApproved:
		return DepositBlockedCustodyBlocked
	default:
		return DepositBlockedCustodyPending
	}
}

// requireNotFrozen refuses a money-moving op once AsaasCustodyEnabled and the
// caller's Asaas subaccount is frozen (plan §7.1) — every money-out path
// (`Withdraw`, `ringTransfer`, `CashoutGame`) must check this before acting;
// silently proceeding would be Invariant #12 territory (money left in limbo
// just because the wallet happens to be frozen instead of merely busy).
// Non-custodied users (or the flag off) have no BaasAccount row and are
// unaffected. Distinct from requireCustodyApproved: that gates the
// deposit/withdrawal entry points on onboarding progress; this gates every
// other money-moving path on the frozen status specifically.
func (s *WalletService) requireNotFrozen(ctx context.Context, userID string) error {
	real, err := s.repo.EnsureRealWallet(ctx, userID)
	if err != nil {
		return err
	}
	if !real.CustodyEnabled {
		return nil
	}
	acc, err := s.baas.GetAccount(ctx, userID)
	if err != nil {
		return err
	}
	if acc != nil && acc.Status == wallet.BaasFrozen {
		return problem.AccountBlocked()
	}
	return nil
}

func (s *WalletService) InitiateDeposit(ctx context.Context, userID, kycLevel string, amount int64, idemKey string) (*wallet.PixDeposit, *pix.Charge, error) {
	if kycLevel == "" {
		return nil, nil, problem.KYCNotVerified()
	}

	// A deposit needs an APPROVED subaccount to open a charge against — else
	// 409 wallet-onboarding so the UI can show the right onboarding step
	// instead of a generic failure. There is no fallback: money deposited by a
	// user is custodied in a subaccount under their own CPF, never in CTech's
	// Inter account (Invariant #14).
	if _, err := s.requireCustodyForDeposit(ctx, userID); err != nil {
		return nil, nil, err
	}

	realw, err := s.repo.EnsureRealWallet(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	if open, err := s.baas.HasOpenMedReceivable(ctx, realw.WalletID); err != nil {
		return nil, nil, err
	} else if open {
		return nil, nil, problem.MedReceivableOpen()
	}
	// Asaas bills every PIX receipt past the monthly free allowance, so a
	// subaccount that has used its allowance stops opening charges rather than
	// silently costing money on each one.
	if err := s.requireReceiptAllowance(ctx, userID); err != nil {
		return nil, nil, err
	}
	// Absolute inbound ceiling — no per-wallet deposit-range override may exceed
	// it. Reject above-cap amounts before any charge is opened at the bank.
	if amount > wallet.MaxInboundAmount {
		return nil, nil, problem.AmountAboveLimit(wallet.MaxInboundAmount)
	}
	// Check the range before opening a charge at the bank — never create a PIX
	// charge for an amount we are going to reject.
	if err := wallet.ValidateDepositAmount(amount, realw); err != nil {
		minAmt, maxAmt := wallet.DepositLimits(realw)
		return nil, nil, problem.DepositOutOfRange(minAmt, maxAmt)
	}

	// SEC-08: register the idempotency key BEFORE opening any charge so a
	// retried POST never opens a second PIX charge. On replay we return the prior
	// deposit + re-query its charge (idempotent).
	rh := reqHash("deposit#"+userID+"#"+idemKey, amount)
	guardPK := wallet.IdemPrefix + "initdep#" + userID + "#" + idemKey
	txid, existing, conflict, err := s.repo.ReserveDepositIdem(ctx, guardPK, id.New(), userID, rh)
	if err != nil {
		return nil, nil, err
	}
	if conflict != nil {
		return nil, nil, conflict
	}
	if existing != nil {
		// Idempotent replay: hand back the original deposit and its charge. A
		// still-unpaid charge is answered from what was stored, because a static
		// QR has no payment at the provider until someone pays it — re-querying
		// one would fail on the most ordinary case there is, a user refreshing
		// the screen before paying.
		if existing.QRCodePayload != "" && existing.Status == wallet.DepositPending {
			return existing, &pix.Charge{
				Txid: existing.Txid, Amount: existing.AmountExpected, QRCode: existing.QRCodePayload,
				QRCodeB64: existing.QRCodeImage, Status: pix.ChargeActive,
			}, nil
		}
		charge, qerr := s.queryDeposit(ctx, existing)
		if qerr != nil {
			return nil, nil, qerr
		}
		return existing, charge, nil
	}

	charge, providerQRCodeID, err := s.baas.CreateDepositCharge(ctx, userID, amount, txid)
	if err != nil {
		slog.Error("pix charge creation failed", "user_id", userID, "txid", txid, "err", err)
		return nil, nil, problem.InternalServer("falha ao criar cobrança PIX")
	}
	dep := &wallet.PixDeposit{
		Txid:             txid,
		WalletID:         realw.WalletID,
		UserID:           userID,
		AmountExpected:   amount,
		Status:           wallet.DepositPending,
		Provider:         wallet.ProviderAsaas,
		ProviderQRCodeID: providerQRCodeID,
		QRCodePayload:    charge.QRCode,
		QRCodeImage:      charge.QRCodeB64,
		CreatedAt:        repositories.NowStr(),
		TTL:              time.Now().Add(depositTTLMinutes * time.Minute).Unix(),
	}
	if err := s.repo.PutDeposit(ctx, dep); err != nil {
		return nil, nil, err
	}
	return dep, charge, nil
}

// queryDeposit re-queries a deposit's charge/payment through whichever
// provider originally opened it (plan §4.3: "payment.pixQrCodeId → txid, not
// the other way round" for the Asaas side; ConfirmDeposit's Invariant #11
// re-query stays provider-agnostic — this is the one place that dispatches).
func (s *WalletService) queryDeposit(ctx context.Context, dep *wallet.PixDeposit) (*pix.Charge, error) {
	if dep.Provider == wallet.ProviderAsaas {
		paymentID := dep.ProviderPaymentID
		if paymentID == "" {
			paymentID = dep.ProviderQRCodeID
		}
		return s.baas.QueryDepositPayment(ctx, dep.UserID, paymentID)
	}
	return s.pix.QueryCharge(ctx, dep.Txid)
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

	charge, err := s.queryDeposit(ctx, dep)
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

	if _, _, err := s.repo.ConfirmDepositCredit(ctx, repositories.Mutation{
		WalletID:       dep.WalletID,
		Amount:         charge.Amount,
		EntryType:      wallet.EntryDeposit,
		Ref:            txid,
		IdempotencyKey: "deposit#" + txid,
		ReqHash:        reqHash(txid, charge.Amount),
	}, txid, charge.E2EID); err != nil {
		return err
	}
	// Meter the receipt AFTER the credit commits, and never let a counter
	// failure fail a deposit that already landed: the worst case is one receipt
	// under-counted against a monthly allowance that has margin built in, which
	// is far cheaper than a confirmed deposit reported as an error and retried.
	// Only Asaas receipts are billed per receipt, so legacy Inter deposits
	// draining after the cutover are not counted.
	if dep.Provider == wallet.ProviderAsaas {
		_, _, month := wallet.WindowKeys(time.Now())
		if err := s.baas.CountReceipt(ctx, dep.UserID, month); err != nil {
			slog.ErrorContext(ctx, "receipt counter update failed", "user_id", dep.UserID, "txid", txid, "err", err)
		}
	}
	s.broadcastDepositConfirmed(ctx, dep.UserID, dep.WalletID, txid, charge.Amount)
	return nil
}

// InitiateClosure runs POST /wallet/closure's state machine (plan §7.2):
// requested → refuse if unsettleable → closing → paid_out. Idempotent: a call
// against an already-closing/subaccount_closed/closed account returns the
// current record rather than restarting.
//
// "Refuse if not settleable" (plan §7.2) is checked here as: no open game
// hold — this codebase's nearest analog to in-flight exposure; multi-party
// settlement batches (plan §6) have no caller anywhere in this codebase yet
// (poker settlement isn't wired), so there is nothing else to check — and no
// open MED receivable (plan §7.3).
//
// Driving the account from `closing` to `subaccount_closed`/
// `closed` once the payout's Asaas transfer confirms DONE is not built here:
// today's transfer-intent/reconcile machinery (plan §6) has no completion
// hook wired to this specific transition yet — flagged as the next increment,
// not a silent gap (the account is left `closing`, never incorrectly marked
// closed early).
func (s *WalletService) InitiateClosure(ctx context.Context, userID, idemKey string) (*wallet.BaasAccount, error) {
	acc, err := s.baas.GetAccount(ctx, userID)
	if err != nil {
		return nil, err
	}
	if acc == nil {
		return nil, problem.WalletOnboarding(wallet.BaasOnboarding)
	}
	switch acc.Status {
	case wallet.BaasClosing, wallet.BaasSubaccountClosed, wallet.BaasClosed:
		return acc, nil // idempotent replay
	case wallet.BaasApproved:
		// proceed
	default:
		return nil, problem.WalletOnboarding(acc.Status)
	}

	realw, err := s.repo.EnsureRealWallet(ctx, userID)
	if err != nil {
		return nil, err
	}
	if _, game, _, err := s.repo.LoadWallets(ctx, userID); err != nil {
		return nil, err
	} else if game != nil {
		holds, err := s.repo.ListOpenHoldsForWallet(ctx, game.WalletID, 1)
		if err != nil {
			return nil, err
		}
		if len(holds) > 0 {
			return nil, problem.Conflict("existe uma sessão em andamento; finalize antes de encerrar a conta")
		}
	}
	if open, err := s.baas.HasOpenMedReceivable(ctx, realw.WalletID); err != nil {
		return nil, err
	} else if open {
		return nil, problem.MedReceivableOpen()
	}

	if err := s.baas.SetAccountStatus(ctx, userID, wallet.BaasClosing); err != nil {
		return nil, err
	}
	if _, err := s.executeWithdrawal(ctx, userID, realw, realw.Balance, idemKey, true, true); err != nil {
		return nil, err
	}
	acc.Status = wallet.BaasClosing
	return acc, nil
}

// ProcessMedClawback debits what's currently available on a custodied user's
// real wallet for a MED clawback event, never going negative (Invariant #1
// stays literal): any shortfall becomes a separate MedReceivable record
// instead (plan §7.3). Idempotent across webhook redelivery — the debit's
// idempotency key/hash are derived from the event ref and the ORIGINAL
// clawback amount (stable across retries), never the dynamically-computed
// debited amount, so a redelivery after balance has already moved is
// recognized as a replay rather than re-derived incorrectly. The receivable
// write always runs regardless of replay status (its own PK is the
// idempotency guard), so a partial failure between the debit and the
// receivable write is safely retried to completion by a later redelivery —
// never left in limbo.
func (s *WalletService) ProcessMedClawback(ctx context.Context, providerAccountID string, amount int64, ref string) error {
	acc, err := s.baas.GetAccountByProviderID(ctx, providerAccountID)
	if err != nil {
		return err
	}
	if acc == nil {
		return nil // unknown account — idempotent no-op, same posture as ConfirmDeposit's unknown-txid handling
	}
	realw, err := s.repo.EnsureRealWallet(ctx, acc.UserID)
	if err != nil {
		return err
	}
	release, err := acquireWallet(ctx, s.lock, realw.WalletID)
	if err != nil {
		return err
	}
	defer release()

	_, _, _, err = s.repo.ApplyMedClawback(ctx, realw.WalletID, acc.UserID, amount, ref, reqHash(ref, amount))
	return err
}

// ConfirmAsaasDeposit is the Asaas payment-webhook counterpart to
// pix-gateway's confirm-deposit call for Inter: it resolves the webhook's
// pixQrCodeId back to the deposit it belongs to (plan §4.3), then defers to
// the existing, provider-agnostic ConfirmDeposit — which re-queries the
// payment itself (via queryDeposit → BaasProvider.QueryDepositPayment) rather
// than trusting this webhook body for money movement (Invariant #11). An
// unresolvable pixQrCodeId is an idempotent no-op, same as ConfirmDeposit's
// own unknown-txid handling.
func (s *WalletService) ConfirmAsaasDeposit(ctx context.Context, paymentID, externalReference string) error {
	if paymentID == "" || externalReference == "" {
		return nil
	}
	dep, err := s.repo.GetDeposit(ctx, externalReference)
	if err != nil {
		return err
	}
	if dep == nil || dep.Provider != wallet.ProviderAsaas {
		return nil
	}
	if dep.ProviderPaymentID != paymentID {
		if err := s.repo.UpdateDepositProviderPaymentID(ctx, dep.Txid, paymentID); err != nil {
			return err
		}
		dep.ProviderPaymentID = paymentID
	}
	// ConfirmDeposit re-queries paymentID and its customer record through Asaas;
	// neither the webhook amount nor webhook customer is used as credit evidence.
	return s.ConfirmDeposit(ctx, dep.Txid, "", "", false)
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
	// The provider is authoritative for whether the money already went back.
	// In particular, Asaas permits partial refunds, so replaying an opaque
	// timeout without observing REFUNDED could create another refund.
	if refunded(charge) {
		return s.markDepositRefunded(ctx, dep)
	}

	var refundErr error
	if dep.Provider == wallet.ProviderAsaas {
		if dep.ProviderPaymentID == "" {
			refundErr = errors.New("asaas: paid deposit has no provider payment id")
		} else {
			refundErr = s.baas.RefundDepositPayment(ctx, dep.UserID, dep.ProviderPaymentID, charge.Amount, "CPF do pagador divergente do CPF cadastrado")
		}
	} else {
		_, refundErr = s.pix.Refund(ctx, charge.E2EID, charge.Amount, "refund#"+dep.Txid)
	}
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

	// With AsaasCustodyEnabled, this user's real money lives in their own Asaas
	// subaccount, not Inter's pooled account — the payout leg below must route
	// there instead (plan §5.2). custodied stays false with the flag off, so
	// every line below behaves exactly as it did before this branch existed.
	acc, err := s.requireCustodyApproved(ctx, userID)
	if err != nil {
		return nil, err
	}
	custodied := acc != nil

	realw, err := s.repo.EnsureRealWallet(ctx, userID)
	if err != nil {
		return nil, err
	}
	if custodied {
		if open, err := s.baas.HasOpenMedReceivable(ctx, realw.WalletID); err != nil {
			return nil, err
		} else if open {
			return nil, problem.MedReceivableOpen()
		}
	}
	return s.executeWithdrawal(ctx, userID, realw, amount, idemKey, custodied, false)
}

// executeWithdrawal is the shared debit/payout core of Withdraw (isClosure
// false) and the closure flow's final payout leg (isClosure true, plan
// §7.2) — same lock/idempotency/debit/dispatch machinery. Callers have already resolved `custodied` and
// any pre-lock gating (custody approval, MED receivables) themselves — this
// function only ever moves money.
func (s *WalletService) executeWithdrawal(ctx context.Context, userID string, realw *wallet.Wallet, amount int64, idemKey string, custodied, isClosure bool) (*wallet.Withdrawal, error) {
	withdrawalID := "withdraw#" + userID + "#" + idemKey
	kyc, err := s.kyc.Get(ctx, userID)
	if err != nil {
		return nil, err
	}
	pixKey := kyc.CPF

	release, err := acquireWallet(ctx, s.lock, realw.WalletID)
	if err != nil {
		return nil, err
	}
	released := false
	defer func() {
		if !released {
			release()
		}
	}()

	if isClosure {
		// Re-read the balance under the lock — the caller's snapshot (taken
		// before acquiring this lock) could be stale. Closure must always pay
		// out exactly what's there NOW: a stale snapshot could either overshoot
		// (rejected by DebitWithdrawal's balance>=amount condition, leaving closure
		// stuck) or undershoot (leaving dust the closure was supposed to sweep).
		fresh, err := s.repo.GetWallet(ctx, realw.WalletID)
		if err != nil {
			return nil, err
		}
		amount = fresh.Balance
		if amount == 0 {
			return &wallet.Withdrawal{
				WithdrawalID: withdrawalID, WalletID: realw.WalletID, UserID: userID, Status: wallet.WithdrawCompleted,
			}, nil
		}
	}

	// Idempotent replay: same key → return the existing withdrawal. Checked
	// under the wallet lock (not before it) so two concurrent identical calls
	// can't both pass this check before either has written anything.
	if existing, err := s.repo.GetWithdrawal(ctx, withdrawalID); err != nil {
		return nil, err
	} else if existing != nil {
		return existing, nil
	}

	rh := reqHash(pixKey, amount)

	// Build the withdrawal record up front so the balance debit, ledger entry,
	// idempotency guard, AND the processing withdrawal row all
	// commit in a single TransactWriteItems. Previously the record was written
	// by a separate PutWithdrawal call, so a transient failure (or a crash)
	// between the two left a committed debit with no processing row — money in
	// limbo, unreconcilable (SEC-01, Invariant #12). Co-writing them makes the
	// debit and its tracking row atomic: on replay the guard AND the record both
	// exist, so GetWithdrawal returns it and there is no orphan (and no nil
	// deref in the handler).
	w := &wallet.Withdrawal{
		WithdrawalID: withdrawalID,
		WalletID:     realw.WalletID,
		UserID:       userID,
		Amount:       amount,
		PixKey:       pixKey,
		Provider: func() string {
			if custodied {
				return wallet.ProviderAsaas
			}
			return ""
		}(),
		Status:         wallet.WithdrawProcessing,
		IdempotencyKey: idemKey,
		CreatedAt:      repositories.NowStr(),
		UpdatedAt:      repositories.NowStr(),
	}
	_, replayed, err := s.repo.DebitWithdrawal(ctx, w, amount, withdrawalID, rh)
	if err != nil {
		return nil, err
	}
	if replayed {
		// The debit itself was a replay (same idempotency key already
		// committed) — someone else is mid-flight on this withdrawal. Never
		// re-transfer; return whatever is on record.
		return s.repo.GetWithdrawal(ctx, withdrawalID)
	}
	// The durable processing row and debit now own convergence. Release the
	// advisory wallet lock before any provider network call so its 10s lease can
	// never expire mid-call and admit overlapping critical sections.
	release()
	released = true

	if custodied {
		// Asaas transfers never confirm synchronously — leg 1 (plan §5.2) is
		// submitted and the withdrawal is always left `processing`; the
		// transfer-authorization webhook + cmd/reconcile drive it to completed
		// once QueryTransfer reports DONE (§6). Submission failure itself is
		// non-fatal here (same posture as the Inter path below): the debit
		// already committed, so this never blocks on it — it just means
		// reconcile has to retry the submission too.
		if err := s.baas.SubmitWithdrawalPayout(ctx, userID, amount, pixKey, withdrawalID); err != nil {
			slog.Warn("asaas withdrawal payout submission failed, left in processing", "withdrawal_id", withdrawalID, "err", err)
		}
		return w, nil
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
	if err := s.requireNotFrozen(ctx, from.UserID); err != nil {
		return nil, nil, err
	}
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
//
// Once the caller's game wallet is Asaas-custodied (plan §9.1a), this also
// settles the real money externally: subaccount → CTech's Asaas master
// account, via the same transfer-intent/authorization/reconcile machinery as
// every other CreateTransfer call in this plan. Pre-custody (or for a
// non-custodied user) it stays exactly as it is today: ledger-only, no
// external call. A settlement submission failure is logged and left for
// cmd/reconcile — the sandbox credits are already granted and real, so this
// never unwinds the already-committed ledger transfer.
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
	if acc, aerr := s.baas.GetIfApproved(ctx, userID); aerr == nil && acc != nil {
		if serr := s.baas.SubmitGamePurchaseSettlement(ctx, userID, credit.SK, amount); serr != nil {
			slog.Error("ALARM asaas game-purchase settlement submission failed; left for reconcile",
				"user_id", userID, "credit_sk", credit.SK, "err", serr)
		}
	}
	return debit, credit, nil
}

// ReverseSandboxGamePurchase undoes an unused game→sandbox purchase (plan
// §9.1a), mirroring §9.2's eligibility rule exactly: refundable iff the
// sandbox wallet has had zero outgoing (debit) ledger entries since
// creditSK, checked with the same AnyDebitSince query §9.2 uses. Reverses the
// ORIGINATING transfer exactly — burns the sandbox credits this specific
// purchase granted, credits `game` back the real-money amount it debited —
// never a general sandbox→game conversion route (Invariant #6): this only
// ever reverses the one named, still-untouched transaction, gated the same
// way §9.2's refund is gated.
//
// Invariant #6 scope note, per root CLAUDE.md's "if a change appears to
// require breaking an invariant, stop and ask": this reversal DOES credit
// `game` (real money) from a sandbox-side operation, which is the literal
// shape Invariant #6 forbids. It does not weaken the invariant in substance —
// no arbitrary sandbox balance is ever spendable as game money, only the one
// specific, still-untouched transaction the caller names, gated by the exact
// same untouched-since-purchase check §9.2 already uses to carve out its own
// narrow exception to the same invariant. The AnyDebitSince gate is the only
// thing standing between "transaction reversal" and "conversion route" — it
// must never be weakened or bypassed.
func (s *WalletService) ReverseSandboxGamePurchase(ctx context.Context, userID, creditSK, idemKey string) (debit, credit *wallet.LedgerEntry, err error) {
	_, game, sandbox, err := s.requireActivated(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	ok, err := s.repo.AnyDebitSince(ctx, sandbox.WalletID, creditSK)
	if err != nil {
		return nil, nil, err
	}
	if ok {
		return nil, nil, problem.SandboxPurchaseUsed()
	}
	amount, err := s.repo.AmountAtSK(ctx, sandbox.WalletID, creditSK)
	if err != nil {
		return nil, nil, err
	}
	credits := wallet.ToSandboxCredits(amount)
	debit, credit, err = s.ringTransfer(ctx, sandbox, game, credits, amount,
		wallet.EntrySandboxPurchaseReversal, wallet.EntryGameFundReversal, "sandbox_purchase_reversal", idemKey,
	)
	if err != nil {
		return nil, nil, err
	}
	if acc, aerr := s.baas.GetIfApproved(ctx, userID); aerr == nil && acc != nil {
		if serr := s.baas.SubmitGamePurchaseReversal(ctx, userID, creditSK, amount); serr != nil {
			slog.Error("ALARM asaas game-purchase reversal submission failed; left for reconcile",
				"user_id", userID, "credit_sk", creditSK, "err", serr)
		}
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
	// Invariant #13 fail-closed kill-switch (plan §6) + frozen-account gate
	// (plan §7.1): a new hold must never be opened against a custodied user
	// whose Asaas balance and ledger already disagree, or whose subaccount
	// is frozen — a not-yet-onboarded user has no BaasAccount row at all and
	// is unaffected.
	if acc, err := s.baas.GetAccount(ctx, userID); err != nil {
		return nil, err
	} else if acc != nil && (acc.ConservationDrift || acc.Status == wallet.BaasFrozen) {
		return nil, problem.AccountBlocked()
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
func (s *WalletService) CashoutGame(ctx context.Context, userID string, amount int64, tableRef string, holdIDs []string, idemKey string) (*wallet.LedgerEntry, error) {
	_, game, _, err := s.requireActivated(ctx, userID)
	if err != nil {
		return nil, err
	}
	if err := s.requireNotFrozen(ctx, userID); err != nil { // plan §7.1 — money-out path
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
		reqHash(tableRef+"#"+strings.Join(holdIDs, ","), amount))
	return entry, err
}

const maxCashoutHolds = 20

// reqHash is the canonical fingerprint guarding "same idempotency key, different
// payload" — the repository compares it and returns idempotency-conflict on drift.
func reqHash(ref string, amount int64) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s|%d", ref, amount)))
	return hex.EncodeToString(h[:])
}
