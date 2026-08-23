package services

import (
	"context"
	"errors"
	"log/slog"

	"gopkg.aoctech.app/api-commons/observability"
	"gopkg.aoctech.app/wallet/api/internal/asaas"
	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/pix"
	"gopkg.aoctech.app/wallet/api/internal/problem"
	"gopkg.aoctech.app/wallet/api/internal/repositories"
)

// depositQRExpirationSeconds is the PIX QR code validity window opened for an
// Asaas-custodied deposit (plan §4.2's own code snippet: 900s).
const depositQRExpirationSeconds = 900

// BaasAccountStore owns the custody account lifecycle.
type BaasAccountStore interface {
	GetBaasAccount(ctx context.Context, userID string) (*wallet.BaasAccount, error)
	GetBaasAccountByProviderID(ctx context.Context, providerAccountID string) (*wallet.BaasAccount, error)
	PutBaasAccount(ctx context.Context, a *wallet.BaasAccount) error
	UpdateBaasAccount(ctx context.Context, userID string, updates map[string]any) error
	ListBaasAccountsByStatus(ctx context.Context, status string, limit int) ([]wallet.BaasAccount, error)
}

// MedReceivableStore owns uncompensated MED clawback records.
type MedReceivableStore interface {
	PutMedReceivableIfAbsent(ctx context.Context, m *wallet.MedReceivable) error
	ListOpenMedReceivablesForWallet(ctx context.Context, walletID string, limit int) ([]wallet.MedReceivable, error)
}

// TransferIntentStore owns idempotent Asaas settlement intents.
type TransferIntentStore interface {
	PutTransferIntentIfAbsent(ctx context.Context, t *wallet.TransferIntent) error
	GetTransferIntent(ctx context.Context, externalReference string) (*wallet.TransferIntent, error)
	UpdateTransferIntent(ctx context.Context, externalReference string, updates map[string]any) error
	ListTransferIntentsByStatus(ctx context.Context, status string, limit int) ([]wallet.TransferIntent, error)
}

// BaasRepo composes the persistence capabilities BaasService orchestrates.
type BaasRepo interface {
	BaasAccountStore
	MedReceivableStore
	TransferIntentStore
}

// WalletReader is the narrow WalletRepository surface BaasService needs: to
// create the real wallet once custody approval lands (plan §3.2 step 6 —
// "a real wallet may not exist before a custody account exists to back it"),
// and to read a user's ledger-side exposure for the Invariant #13
// conservation check (plan §6).
type WalletReader interface {
	EnsureRealWallet(ctx context.Context, userID string) (*wallet.Wallet, error)
	LoadWallets(ctx context.Context, userID string) (real, game, sandbox *wallet.Wallet, err error)
	ListOpenHoldsForWallet(ctx context.Context, walletID string, limit int) ([]wallet.Hold, error)
	// UpdateWithdrawal lets ReconcileTransferIntents mark a withdrawal completed
	// once its Asaas payout leg confirms DONE (plan §6) — the async
	// counterpart to WalletService.Withdraw's synchronous Inter completion.
	UpdateWithdrawal(ctx context.Context, withdrawalID string, updates map[string]any) error
}

// conservationSweepLimit bounds each conservation-check sweep's account/hold
// pages — a background job, not a request path, but still never an unbounded
// table scan (plan §6, repositories package convention).
const conservationSweepLimit = 500

// BaasService owns Asaas custody: onboarding, the account-status and
// transfer-authorization webhook decisions, and the shared settlement-leg
// submission primitive every money-out-to-Asaas call site in the plan uses.
type BaasService struct {
	repo           BaasRepo
	wallets        WalletReader
	asaas          asaas.AsaasClient
	audit          Auditor
	kyc            KYCClient
	masterKey      []byte
	parentWalletID string
	// parentAPIKey is CTech's OWN Asaas parent-account API key — used only by
	// the §9.1a reversal leg, which authenticates as the parent (moving money
	// FROM it), the mirror image of every other call in this file (which
	// authenticates as the subaccount). Empty is fine for every environment
	// that never reverses a game-funded sandbox purchase against a custodied
	// user (i.e. everywhere until AsaasCustodyEnabled is on).
	parentAPIKey      string
	reverseWithdrawal func(context.Context, string) error
}

func (b *BaasService) SetWithdrawalReverser(fn func(context.Context, string) error) {
	b.reverseWithdrawal = fn
}

func NewBaasService(repo BaasRepo, wallets WalletReader, asaasClient asaas.AsaasClient, audit Auditor, kyc KYCClient, masterKey []byte, parentWalletID, parentAPIKey string) *BaasService {
	return &BaasService{
		repo: repo, wallets: wallets, asaas: asaasClient, audit: audit, kyc: kyc,
		masterKey: masterKey, parentWalletID: parentWalletID, parentAPIKey: parentAPIKey,
	}
}

// GetIfApproved returns the caller's BaasAccount iff its custody status is
// approved — nil, nil otherwise (absent, still onboarding, frozen, closed).
// This is the single per-user gate every conditional Asaas branch in this
// codebase reads (plan §4.1, §5.1, §9.1a): pre-migration, or for any user who
// never onboarded, it is always nil, nil — exactly the "behave exactly as
// today" fallback those call sites depend on.
func (b *BaasService) GetIfApproved(ctx context.Context, userID string) (*wallet.BaasAccount, error) {
	acc, err := b.repo.GetBaasAccount(ctx, userID)
	if err != nil {
		return nil, err
	}
	if acc == nil || acc.Status != wallet.BaasApproved {
		return nil, nil
	}
	return acc, nil
}

// SubmitWithdrawalPayout fires leg 1 of a custodied withdrawal (plan §5.2): a
// PIX transfer from the user's own subaccount to their own registered CPF
// key. Reuses SubmitTransfer's shared intent-write-then-CreateTransfer
// primitive, so it inherits the same idempotent-by-ExternalReference,
// non-fatal-on-submission-failure contract every other Asaas transfer leg in
// this plan has.
func (b *BaasService) SubmitWithdrawalPayout(ctx context.Context, userID string, amount int64, pixKeyCPF, withdrawalID string) error {
	acc, err := b.repo.GetBaasAccount(ctx, userID)
	if err != nil {
		return err
	}
	if acc == nil || acc.Status != wallet.BaasApproved {
		return errors.New("asaas: subaccount not approved")
	}
	apiKey, err := b.DecryptAPIKey(acc)
	if err != nil {
		return err
	}
	req := asaas.TransferRequest{
		Value: amount, PixAddressKey: pixKeyCPF, PixAddressKeyType: asaas.PixKeyTypeCPF,
		ExternalReference: withdrawalID + "#payout",
	}
	return b.SubmitTransfer(ctx, wallet.IntentKindWithdrawalPayout, userID, apiKey, req, withdrawalID)
}

// CheckConservation compares userID's Asaas subaccount balance to the sum of
// their own custody-backed ledger exposure: real.Balance + game.Balance +
// open game-wallet holds (Invariant #13, plan §6). drift == 0 means
// conserved; any other value means Asaas's system of record and this ledger
// disagree. A non-approved (or absent) account is trivially conserved — there
// is nothing custodied yet to compare.
func (b *BaasService) CheckConservation(ctx context.Context, userID string) (drift int64, err error) {
	acc, err := b.repo.GetBaasAccount(ctx, userID)
	if err != nil {
		return 0, err
	}
	if acc == nil || acc.Status != wallet.BaasApproved {
		return 0, nil
	}
	apiKey, err := b.DecryptAPIKey(acc)
	if err != nil {
		return 0, err
	}
	balance, err := b.asaas.QueryAccountBalance(ctx, apiKey)
	if err != nil {
		return 0, err
	}

	real, game, _, err := b.wallets.LoadWallets(ctx, userID)
	if err != nil {
		return 0, err
	}
	var ledgerTotal int64
	if real != nil {
		ledgerTotal += real.Balance
	}
	if game != nil {
		ledgerTotal += game.Balance
		holds, err := b.wallets.ListOpenHoldsForWallet(ctx, game.WalletID, conservationSweepLimit)
		if err != nil {
			return 0, err
		}
		for _, h := range holds {
			ledgerTotal += h.Amount
		}
	}
	return ledgerTotal - balance, nil
}

// RunConservationCheck walks every approved BaaS account and sets/clears
// ConservationDrift accordingly — the fail-closed kill-switch Withdraw,
// InitiateDeposit, and HoldGame all read before acting on a custodied user
// (plan §6). Returns how many accounts were checked and how many are (now)
// drifted. A read failure for one account is logged and skipped, never
// flagged as drift — only a confirmed mismatch sets the switch.
func (b *BaasService) RunConservationCheck(ctx context.Context) (checked, drifted int, err error) {
	accounts, err := b.repo.ListBaasAccountsByStatus(ctx, wallet.BaasApproved, conservationSweepLimit)
	if err != nil {
		return 0, 0, err
	}
	for _, acc := range accounts {
		checked++
		drift, cerr := b.CheckConservation(ctx, acc.UserID)
		if cerr != nil {
			slog.Error("conservation check failed", "user_id", acc.UserID, "err", cerr)
			continue
		}
		isDrifted := drift != 0
		if isDrifted {
			drifted++
		}
		if isDrifted == acc.ConservationDrift {
			continue
		}
		if isDrifted {
			slog.Error("ALARM Invariant #13 conservation drift detected", "user_id", acc.UserID, "drift", drift)
		}
		if err := b.repo.UpdateBaasAccount(ctx, acc.UserID, map[string]any{"conservation_drift": isDrifted}); err != nil {
			slog.Error("conservation drift flag update failed", "user_id", acc.UserID, "err", err)
		}
	}
	return checked, drifted, nil
}

// ReconcileTransferIntents walks every wallet_transfer_intents row still
// awaiting_authorization or processing, re-queries Asaas by ExternalReference,
// and drives it to done (retrying submission if it never reached Asaas at
// all) or leaves it for the next run — the async safety net every §5.2/§6/
// §9.1a settlement leg in this plan depends on, same shape as
// WalletService.ReconcileWithdrawals for the Inter side. A submission retry
// is idempotent by construction (ExternalReference, Invariant #3), so a leg
// that DID reach Asaas but whose response was lost is never resubmitted.
func (b *BaasService) ReconcileTransferIntents(ctx context.Context) (resolved, retried, alarmed int, err error) {
	for _, status := range []string{wallet.IntentAwaitingAuthorization, wallet.IntentProcessing} {
		intents, lerr := b.repo.ListTransferIntentsByStatus(ctx, status, conservationSweepLimit)
		if lerr != nil {
			return resolved, retried, alarmed, lerr
		}
		for i := range intents {
			it := intents[i]
			r, ret, alm := b.reconcileOneIntent(ctx, it)
			resolved += r
			retried += ret
			alarmed += alm
		}
	}
	return resolved, retried, alarmed, nil
}

func (b *BaasService) reconcileOneIntent(ctx context.Context, it wallet.TransferIntent) (resolved, retried, alarmed int) {
	acc, err := b.repo.GetBaasAccount(ctx, it.UserID)
	if err != nil || acc == nil {
		slog.Error("reconcile: intent lookup failed to resolve baas account", "external_reference", it.ExternalReference, "err", err)
		return 0, 0, 1
	}
	apiKey, err := b.DecryptAPIKey(acc)
	if err != nil {
		slog.Error("reconcile: decrypt api key failed", "external_reference", it.ExternalReference, "err", err)
		return 0, 0, 1
	}

	transfer, err := b.asaas.QueryTransfer(ctx, apiKey, it.ExternalReference)
	if err != nil && !errors.Is(err, asaas.ErrTransferNotFound) {
		// An ambiguous query failure cannot prove that the original submission
		// was absent. Never resubmit in that state: doing so could duplicate a
		// real-money payout if provider idempotency regressed or was unavailable.
		slog.Error("reconcile: transfer query failed; refusing ambiguous resubmission", "external_reference", it.ExternalReference, "err", err)
		return 0, 0, 1
	}
	if errors.Is(err, asaas.ErrTransferNotFound) || transfer == nil {
		// The provider positively reported that the external reference is absent.
		if b.retrySubmission(ctx, it, apiKey) {
			return 0, 1, 0
		}
		return 0, 0, 1
	}
	switch transfer.Status {
	case asaas.TransferDone:
		if err := b.MarkTransferDone(ctx, it.ExternalReference, transfer.ID, transfer.TransferFee); err != nil {
			slog.Error("reconcile: mark transfer done failed", "external_reference", it.ExternalReference, "err", err)
			return 0, 0, 1
		}
		if it.Kind == wallet.IntentKindWithdrawalPayout {
			withdrawalID := it.Ref
			if err := b.wallets.UpdateWithdrawal(ctx, withdrawalID, map[string]any{"status": wallet.WithdrawCompleted, "e2e_id": transfer.ID}); err != nil {
				slog.Error("reconcile: mark withdrawal completed failed", "withdrawal_id", withdrawalID, "err", err)
				return 0, 0, 1
			}
		}
		return 1, 0, 0
	case asaas.TransferCancelled, asaas.TransferFailed:
		// Asaas auto-cancelled after 3 failed authorization responses, or the
		// transfer otherwise failed at the bank (plan §2.3). For a withdrawal
		// payout this mirrors the Inter path's pix.TransferNotFound case — the
		// debit already committed and must be reversed, same Invariant #12
		// contract as WalletService.reverse. Other kinds (fee sweep, settlement,
		// sandbox-purchase settlement) have no symmetric "reverse the ledger"
		// action defined yet — alarmed for manual follow-up rather than guessed at.
		if it.Kind == wallet.IntentKindWithdrawalPayout && b.reverseWithdrawal != nil {
			if err := b.reverseWithdrawal(ctx, it.Ref); err != nil {
				slog.Error("ALARM asaas withdrawal reversal failed", "withdrawal_id", it.Ref, "err", err)
				return 0, 0, 1
			}
			if updateErr := b.repo.UpdateTransferIntent(ctx, it.ExternalReference, map[string]any{"status": wallet.IntentFailed}); updateErr != nil {
				observability.Error(ctx, "asaas failed transfer status update failed", updateErr, "external_reference", it.ExternalReference)
			}
			return 0, 0, 0
		}
		slog.Error("ALARM asaas transfer cancelled/failed", "external_reference", it.ExternalReference, "kind", it.Kind, "status", transfer.Status)
		return 0, 0, 1
	default:
		// Still pending at Asaas — leave for the next run.
		return 0, 0, 0
	}
}

// retrySubmission resubmits a transfer that never reached Asaas. Amount and
// destination are read back from the intent row itself (the same values the
// authorization webhook would have compared against), so this never guesses
// at what the original request looked like.
func (b *BaasService) retrySubmission(ctx context.Context, it wallet.TransferIntent, apiKey string) bool {
	req := asaas.TransferRequest{Value: it.Amount, ExternalReference: it.ExternalReference}
	if len(it.Destination) > 0 {
		if it.DestinationType == wallet.TransferDestinationPIX {
			req.PixAddressKey, req.PixAddressKeyType = it.Destination, asaas.PixKeyTypeCPF
		} else if it.DestinationType == wallet.TransferDestinationWallet {
			req.WalletID = it.Destination
		} else {
			slog.Error("reconcile: transfer intent missing destination type", "external_reference", it.ExternalReference)
			return false
		}
	}
	if _, err := b.asaas.CreateTransfer(ctx, apiKey, req); err != nil {
		slog.Warn("reconcile: transfer resubmission failed, will retry next run", "external_reference", it.ExternalReference, "err", err)
		return false
	}
	return true
}

// SubmitGamePurchaseSettlement fires §9.1a's forward settlement leg for a
// game-funded sandbox purchase (PurchaseSandbox): subaccount → CTech's Asaas
// master account. ExternalReference is derived from creditSK alone — the
// sandbox ledger credit's own sort key, unique per purchase — so both this
// leg and its eventual reversal (SubmitGamePurchaseReversal) can be found
// deterministically without a new lookup table or GSI.
func (b *BaasService) SubmitGamePurchaseSettlement(ctx context.Context, userID, creditSK string, amount int64) error {
	acc, err := b.repo.GetBaasAccount(ctx, userID)
	if err != nil {
		return err
	}
	if acc == nil || acc.Status != wallet.BaasApproved {
		return errors.New("asaas: subaccount not approved")
	}
	apiKey, err := b.DecryptAPIKey(acc)
	if err != nil {
		return err
	}
	req := asaas.TransferRequest{Value: amount, WalletID: b.parentWalletID, ExternalReference: "sbxg#" + creditSK}
	return b.SubmitTransfer(ctx, wallet.IntentKindSandboxPurchaseSettle, userID, apiKey, req, creditSK)
}

// SubmitGamePurchaseReversal fires §9.1a's reversal leg — but only if the
// forward leg (looked up deterministically by "sbxg#"+creditSK) already
// reached DONE. If it never did, marks it superseded and stops: no money
// ever left the subaccount, so there is nothing to reverse externally. Named
// residual risk, same posture as §6's frozen-counterparty gap: a race where
// the forward leg reaches DONE at Asaas microseconds after being marked
// superseded locally is possible but narrow (the convergence window is
// minutes) — cmd/reconcile's conservation scan (§6) is the backstop that
// catches it, not new machinery.
//
// This leg moves money the opposite direction of every other Asaas call in
// this codebase (parent → subaccount, not subaccount → parent/subaccount →
// PIX-key), so it authenticates as the PARENT account (b.parentAPIKey), never
// the subaccount's own key.
func (b *BaasService) SubmitGamePurchaseReversal(ctx context.Context, userID, creditSK string, amount int64) error {
	forwardRef := "sbxg#" + creditSK
	forward, err := b.repo.GetTransferIntent(ctx, forwardRef)
	if err != nil {
		return err
	}
	if forward == nil || forward.Status != wallet.IntentDone {
		if forward != nil {
			return b.repo.UpdateTransferIntent(ctx, forwardRef, map[string]any{"status": wallet.IntentSuperseded})
		}
		return nil
	}
	acc, err := b.repo.GetBaasAccount(ctx, userID)
	if err != nil {
		return err
	}
	if acc == nil || acc.Status != wallet.BaasApproved {
		return errors.New("asaas: subaccount not approved")
	}
	req := asaas.TransferRequest{Value: amount, WalletID: acc.ProviderWalletID, ExternalReference: "sbxg-rev#" + creditSK}
	return b.SubmitTransfer(ctx, wallet.IntentKindSandboxPurchaseReverse, userID, b.parentAPIKey, req, creditSK)
}

// GetAccount returns the caller's BaasAccount regardless of status, or nil if
// absent — the read GetBalances' onboarding-state branch needs (plan §4.1),
// as opposed to GetIfApproved's narrower "approved or nil" contract.
func (b *BaasService) GetAccount(ctx context.Context, userID string) (*wallet.BaasAccount, error) {
	return b.repo.GetBaasAccount(ctx, userID)
}

// GetAccountByProviderID resolves an Asaas account.id back to its
// BaasAccount row — nil if unknown (plan §7.1, §7.3 webhook dispatch).
func (b *BaasService) GetAccountByProviderID(ctx context.Context, providerAccountID string) (*wallet.BaasAccount, error) {
	return b.repo.GetBaasAccountByProviderID(ctx, providerAccountID)
}

// PutMedReceivableIfAbsent records a MED clawback shortfall (plan §7.3).
func (b *BaasService) PutMedReceivableIfAbsent(ctx context.Context, m *wallet.MedReceivable) error {
	return b.repo.PutMedReceivableIfAbsent(ctx, m)
}

// HasOpenMedReceivable reports whether walletID has an outstanding MED
// clawback debt (plan §7.3 point 3).
func (b *BaasService) HasOpenMedReceivable(ctx context.Context, walletID string) (bool, error) {
	open, err := b.repo.ListOpenMedReceivablesForWallet(ctx, walletID, 1)
	if err != nil {
		return false, err
	}
	return len(open) > 0, nil
}

// SetAccountStatus transitions a BaasAccount's lifecycle status directly —
// used by the closure state machine (plan §7.2).
func (b *BaasService) SetAccountStatus(ctx context.Context, userID, status string) error {
	return b.repo.UpdateBaasAccount(ctx, userID, map[string]any{"status": status})
}

// CreateDepositCharge opens a PIX QR code against the caller's own Asaas
// subaccount, using its cached static EVP key (plan §4.2). Only ever called
// once WalletService.InitiateDeposit has already confirmed via GetIfApproved
// that the subaccount is approved — an unapproved/absent account here is a
// caller error, not a recoverable state, so it errors rather than silently
// falling back to Inter.
func (b *BaasService) CreateDepositCharge(ctx context.Context, userID string, amount int64, txid string) (*pix.Charge, string, error) {
	acc, err := b.repo.GetBaasAccount(ctx, userID)
	if err != nil {
		return nil, "", err
	}
	if acc == nil || acc.Status != wallet.BaasApproved {
		return nil, "", errors.New("asaas: subaccount not approved")
	}
	apiKey, err := b.DecryptAPIKey(acc)
	if err != nil {
		return nil, "", err
	}
	qr, err := b.asaas.CreatePixQRCode(ctx, apiKey, asaas.QRCodeRequest{
		AddressKey: acc.EVPPixKey, Value: amount, Format: "ALL",
		ExpirationSeconds: depositQRExpirationSeconds, AllowsMultiplePayments: false, ExternalReference: txid,
	})
	if err != nil {
		return nil, "", err
	}
	charge := &pix.Charge{Txid: txid, Amount: amount, QRCode: qr.Payload, QRCodeB64: qr.EncodedImage, Status: pix.ChargeActive}
	return charge, qr.PixQRCodeID, nil
}

// QueryDepositPayment re-queries an Asaas payment and then its linked customer
// through the same subaccount credential. The webhook only supplies the payment
// ID wake-up; amount, status, customer linkage and CPF come from Asaas reads.
func (b *BaasService) QueryDepositPayment(ctx context.Context, userID, paymentID string) (*pix.Charge, error) {
	acc, err := b.repo.GetBaasAccount(ctx, userID)
	if err != nil {
		return nil, err
	}
	if acc == nil {
		return nil, errors.New("asaas: no baas account for deposit query")
	}
	apiKey, err := b.DecryptAPIKey(acc)
	if err != nil {
		return nil, err
	}
	payment, err := b.asaas.QueryPayment(ctx, apiKey, paymentID)
	if err != nil {
		return nil, err
	}
	status := pix.ChargeActive
	if payment.Status == asaas.PaymentReceived {
		status = pix.ChargeCompleted
	}
	charge := &pix.Charge{Txid: payment.ExternalReference, Amount: payment.Value, Status: status, E2EID: payment.ID}
	if payment.Status == asaas.PaymentRefunded {
		// The Asaas payment status is authoritative that its full PIX payment
		// was returned. The synthetic stable ID is only a local ledger replay
		// key and is never sent back to the provider.
		charge.Status = pix.ChargeCompleted
		charge.Refunds = []pix.Refund{{RtrID: "asaas#" + payment.ID, Amount: payment.Value, Status: pix.RefundCompleted}}
	}
	if payment.CustomerID == "" {
		return charge, nil
	}
	customer, err := b.asaas.QueryCustomer(ctx, apiKey, payment.CustomerID)
	if err != nil {
		return nil, err
	}
	charge.PayerCPF = customer.CPFCNPJ
	return charge, nil
}

// RefundDepositPayment returns a paid Asaas PIX charge from the receiving
// subaccount, not CTech's parent account. The caller persists refund_pending
// first and reconciles retry decisions via QueryPayment's REFUNDED state.
func (b *BaasService) RefundDepositPayment(ctx context.Context, userID, paymentID string, amount int64, reason string) error {
	acc, err := b.repo.GetBaasAccount(ctx, userID)
	if err != nil {
		return err
	}
	if acc == nil || acc.Status != wallet.BaasApproved {
		return errors.New("asaas: subaccount not approved")
	}
	apiKey, err := b.DecryptAPIKey(acc)
	if err != nil {
		return err
	}
	return b.asaas.RefundPayment(ctx, apiKey, paymentID, amount, reason)
}

// DecryptAPIKey recovers a subaccount's plaintext Asaas API key from its
// AES-256-GCM ciphertext (plan §3.3).
func (b *BaasService) DecryptAPIKey(acc *wallet.BaasAccount) (string, error) {
	return asaas.DecryptAPIKey(b.masterKey, acc.APIKeyCiphertext, acc.APIKeyNonce)
}

// AuthorizeTransfer answers Asaas's synchronous transfer-authorization
// webhook (plan §2.3): one GetItem by externalReference, no outbound calls —
// the latency budget is tight, since 3 failed responses auto-cancel the
// transfer. Approves iff a stored intent exists and its amount and
// destination match the callback exactly; any mismatch or unknown reference
// is refused. This is the single choke point that would catch a transfer
// built for the wrong amount or destination before Asaas ever moves money.
func (b *BaasService) AuthorizeTransfer(ctx context.Context, externalReference string, amount int64, destination string) (approved bool, refuseReason string) {
	intent, err := b.repo.GetTransferIntent(ctx, externalReference)
	if err != nil {
		slog.Error("asaas transfer-authorization: lookup failed", "external_reference", externalReference, "err", err)
		return false, "lookup_failed"
	}
	if intent == nil {
		return false, "unknown_reference"
	}
	// Destination is mandatory on both sides. Missing provider data is not a
	// wildcard: payload/schema drift must refuse the transfer, never bypass the
	// most important authorization comparison.
	if externalReference == "" || amount <= 0 || intent.Amount != amount ||
		intent.Destination == "" || destination == "" || intent.Destination != destination {
		slog.Error("ALARM asaas transfer-authorization mismatch", "external_reference", externalReference,
			"expected_amount", intent.Amount, "got_amount", amount, "expected_destination", intent.Destination, "got_destination", destination)
		return false, "mismatch"
	}
	if err := b.repo.UpdateTransferIntent(ctx, externalReference, map[string]any{"status": wallet.IntentProcessing}); err != nil {
		slog.Error("asaas transfer-authorization: status update failed", "external_reference", externalReference, "err", err)
		return false, "internal_error"
	}
	return true, ""
}

// MarkTransferDone records that a transfer-intent's leg completed — called
// once the reconcile job (or an eventual settlement webhook) confirms
// QueryTransfer/asaas.Transfer.Status == asaas.TransferDone.
func (b *BaasService) MarkTransferDone(ctx context.Context, externalReference, providerTransferID string, transferFee int64) error {
	return b.repo.UpdateTransferIntent(ctx, externalReference, map[string]any{
		"status":               wallet.IntentDone,
		"provider_transfer_id": providerTransferID,
		"transfer_fee":         transferFee,
	})
}

// SubmitTransfer writes the tracking intent row THEN calls CreateTransfer —
// the shared "settlement leg" primitive every money-out-to-Asaas call site in
// the plan uses (§5.2 payout/fee-sweep legs, §6 batch legs, §9.1a purchase
// settlement/reversal). Ledger truth must already be committed by the caller
// before this runs, never the other way round. A submission failure here is
// non-fatal to whatever ledger mutation already committed — it is logged and
// alarmed, and the row is left `awaiting_authorization` for cmd/reconcile to
// retry; ExternalReference is the idempotency key (Invariant #3), so a
// retried CreateTransfer never double-submits.
func (b *BaasService) SubmitTransfer(ctx context.Context, kind, userID, apiKey string, req asaas.TransferRequest, ref string) error {
	destination := req.WalletID
	destinationType := wallet.TransferDestinationWallet
	if destination == "" {
		destination = req.PixAddressKey
		destinationType = wallet.TransferDestinationPIX
	}
	intent := &wallet.TransferIntent{
		ExternalReference: req.ExternalReference,
		Kind:              kind,
		Status:            wallet.IntentAwaitingAuthorization,
		UserID:            userID,
		Amount:            req.Value,
		Destination:       destination,
		DestinationType:   destinationType,
		Ref:               ref,
		CreatedAt:         repositories.NowStr(),
		UpdatedAt:         repositories.NowStr(),
	}
	if err := b.repo.PutTransferIntentIfAbsent(ctx, intent); err != nil {
		if errors.Is(err, repositories.ErrTransferIntentExists) {
			existing, getErr := b.repo.GetTransferIntent(ctx, req.ExternalReference)
			if getErr != nil {
				return getErr
			}
			if existing == nil || existing.Kind != kind || existing.UserID != userID || existing.Amount != req.Value || existing.Destination != destination || existing.DestinationType != destinationType || existing.Ref != ref {
				return problem.IdempotencyConflict()
			}
			return nil // exact replay — never re-send
		}
		slog.Error("ALARM asaas transfer intent write failed", "external_reference", req.ExternalReference, "kind", kind, "err", err)
		return err
	}
	if _, err := b.asaas.CreateTransfer(ctx, apiKey, req); err != nil {
		slog.Error("ALARM asaas CreateTransfer submission failed; left awaiting_authorization for reconcile",
			"external_reference", req.ExternalReference, "kind", kind, "err", err)
		return err
	}
	return nil
}

// InitiateOnboarding opens a user's Asaas subaccount (plan §3.2). Gates:
// verified KYC, mirroring ActivateGambling's gate — real custody is about to
// be opened. Idempotent on userID: a second call while a row already exists,
// in ANY status, returns that record unchanged rather than issuing a second
// POST /v3/accounts (mirrors EnsureRealWallet's create-or-reuse pattern).
//
// incomeValue is an Asaas cadastral field, not an identity attribute — it is
// sent to Asaas and never persisted here (plan §3.1: no documented LGPD
// retention need under arts. 6º/9º/18). Do NOT add a database column for it.
//
// Deliberately does NOT call EnsureRealWallet: "a real wallet may not exist
// before a custody account exists to back it, otherwise Invariant #13 is
// false from birth" (plan §3.2 step 5). The real wallet is created later, by
// ProcessAccountStatusWebhook, once the subaccount reaches ACCOUNT_STATUS_APPROVED.
func (b *BaasService) InitiateOnboarding(ctx context.Context, userID, kycLevel string, incomeValue int64) (*wallet.BaasAccount, error) {
	if kycLevel != wallet.KYCVerified {
		return nil, problem.KYCNotVerified()
	}
	existing, err := b.repo.GetBaasAccount(ctx, userID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}
	now := repositories.NowStr()
	reservation := &wallet.BaasAccount{UserID: userID, Status: wallet.BaasOnboarding, CreatedAt: now, UpdatedAt: now}
	if err := b.repo.PutBaasAccount(ctx, reservation); err != nil {
		if errors.Is(err, repositories.ErrBaasAccountExists) {
			return b.repo.GetBaasAccount(ctx, userID)
		}
		return nil, err
	}

	kyc, err := b.kyc.Get(ctx, userID)
	if err != nil {
		return nil, err
	}
	acc, err := b.asaas.CreateAccount(ctx, b.parentAPIKey, asaas.CreateAccountRequest{
		Name: kyc.LegalName, CPF: kyc.CPF, Email: kyc.Email, MobilePhone: kyc.Phone,
		BirthDate: kyc.BirthDate, Address: kyc.Address.Street, AddressNumber: kyc.Address.Number,
		Complement: kyc.Address.Complement, Province: kyc.Address.District, City: kyc.Address.City, State: kyc.Address.State,
		PostalCode: kyc.Address.ZipCode, IncomeValue: incomeValue,
	})
	if err != nil {
		slog.Error("asaas account creation failed", "user_id", userID, "err", err)
		return nil, problem.InternalServer("falha ao criar subconta de custódia")
	}

	ciphertext, nonce, err := asaas.EncryptAPIKey(b.masterKey, acc.APIKey)
	if err != nil {
		return nil, err
	}

	row := &wallet.BaasAccount{
		UserID:            userID,
		Status:            wallet.BaasOnboarding,
		ProviderAccountID: acc.ID,
		ProviderWalletID:  acc.WalletID,
		APIKeyCiphertext:  ciphertext,
		APIKeyNonce:       nonce,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := b.repo.UpdateBaasAccount(ctx, userID, map[string]any{
		"provider_account_id": acc.ID, "provider_wallet_id": acc.WalletID,
		"api_key_ciphertext": ciphertext, "api_key_nonce": nonce,
	}); err != nil {
		return nil, err
	}
	if b.audit != nil {
		if err := b.audit.Append(ctx, &wallet.AuditEvent{
			UserID: userID, EventType: wallet.EventBaasSubaccountCreated, Actor: userID, After: acc.ID,
		}); err != nil {
			return nil, err
		}
	}
	return row, nil
}

// ProcessAccountStatusWebhook handles Asaas's account-status callback (plan
// §3.2 step 6, §7.1). An unknown provider account ID is an idempotent no-op —
// mirrors ConfirmDeposit's handling of an unrecognized txid. Any status other
// than approved/rejected is recorded as pending_approval; §7.1's frozen state
// is driven by a distinct balance-block webhook category, not this one.
func (b *BaasService) ProcessAccountStatusWebhook(ctx context.Context, providerAccountID, status string) error {
	acc, err := b.repo.GetBaasAccountByProviderID(ctx, providerAccountID)
	if err != nil {
		return err
	}
	if acc == nil {
		return nil
	}
	switch status {
	case asaas.AccountStatusApproved:
		return b.handleAccountApproved(ctx, acc)
	case asaas.AccountStatusRejected:
		return b.repo.UpdateBaasAccount(ctx, acc.UserID, map[string]any{"status": wallet.BaasClosed})
	default:
		return b.repo.UpdateBaasAccount(ctx, acc.UserID, map[string]any{"status": wallet.BaasPendingApproval})
	}
}

// handleAccountApproved creates the subaccount's static EVP PIX key exactly
// once, ever (plan §3.2 step 6 — never re-derived, cached from here on), then
// creates the real wallet and audits activation. Idempotent: an
// already-approved account is a no-op, so a redelivered webhook never
// re-creates the EVP key or re-audits.
func (b *BaasService) handleAccountApproved(ctx context.Context, acc *wallet.BaasAccount) error {
	if acc.Status == wallet.BaasApproved {
		return nil
	}
	apiKey, err := b.DecryptAPIKey(acc)
	if err != nil {
		return err
	}
	evpKey := acc.EVPPixKey
	if evpKey == "" {
		key, err := b.asaas.CreateStaticPixKey(ctx, apiKey)
		if err != nil {
			// Left in pending_approval — a webhook redelivery (or an ops retry) will
			// attempt this again; never invent a wallet without the key it needs to
			// receive PIX deposits.
			slog.Error("asaas EVP key creation failed", "user_id", acc.UserID, "err", err)
			return err
		}
		evpKey = key.Key
	}
	if err := b.repo.UpdateBaasAccount(ctx, acc.UserID, map[string]any{
		"status": wallet.BaasApproved, "evp_pix_key": evpKey,
	}); err != nil {
		return err
	}
	if _, err := b.wallets.EnsureRealWallet(ctx, acc.UserID); err != nil {
		return err
	}
	if b.audit == nil {
		return nil
	}
	return b.audit.Append(ctx, &wallet.AuditEvent{
		UserID: acc.UserID, EventType: wallet.EventWalletActivated, Actor: "asaas_webhook", After: wallet.BaasApproved,
	})
}
