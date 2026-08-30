// Package wallet holds the wallet domain model: the two balance types, ledger
// entry kinds, deposit/withdrawal statuses, table/index names, and money math.
// Every string and numeric key lives here as a named constant.
package wallet

import rpccontract "gopkg.aoctech.app/wallet/rpc-contract"

// Wallet balance types. `game` holds REAL money earmarked for games: it is
// withdrawable (via `real`) and counts toward the user's real holdings. It exists
// so personal gambling limits have exactly one edge to meter — `real → game`.
// `sandbox` is virtual and remains a sink (Invariant #6).
const (
	TypeReal    = "real"
	TypeGame    = "game"
	TypeSandbox = "sandbox"
)

// Ledger entry types (see design spec §A).
const (
	EntryDeposit         = "deposit"
	EntryWithdraw        = "withdraw"
	EntryGameDebit       = "game_debit"
	EntryGameCredit      = "game_credit"
	EntrySandboxPurchase = "sandbox_purchase"
	EntrySandboxCredit   = "sandbox_credit"
	EntryReversal        = "reversal"       // credit-back of a failed withdrawal
	EntryDepositRefund   = "deposit_refund" // debit reversing a deposit later returned to the payer (devolução)

	// Ring-fence transfers between `real` and `game`. Funding is metered by the
	// personal limit engine; returning is always free and never limited.
	EntryGameFundDebit    = "game_fund_debit"    // debit real
	EntryGameFundCredit   = "game_fund_credit"   // credit game
	EntryGameReturnDebit  = "game_return_debit"  // debit game
	EntryGameReturnCredit = "game_return_credit" // credit real

	EntryBillingDebit = "billing_debit" // real debited by an authorized M2M client (ctech-billing)

	// EntryMedClawback debits what's currently available when Asaas reports a
	// MED (Mecanismo Especial de Devolução) clawback (plan §7.3). Never goes
	// below zero — any shortfall becomes a separate MedReceivable record.
	EntryMedClawback = "med_clawback"

	// §9.1a — reversal of a game-funded sandbox purchase (PurchaseSandbox).
	// Deliberately distinct from EntryGameReturnDebit/Credit (ReturnFromGame):
	// that is a user CHOOSING to exit the ring-fence; this UNDOES one specific,
	// still-untouched purchase transaction. Conflating the two would make the
	// audit trail unreadable and would double-count against Invariant #8's
	// gross-inflow limit accounting.
	EntrySandboxPurchaseReversal = "sandbox_purchase_reversal" // debit sandbox
	EntryGameFundReversal        = "game_fund_reversal"        // credit game

	// §9.2 — revoking unused credits from a direct PIX sandbox purchase
	// (wallet_sandbox_purchases, decoupled from the ring-fence entirely). A
	// debit that zeroes out the entitlement — never a conversion: sandbox
	// credits are simply revoked, they never become game or real money.
	EntrySandboxRefundReversal = "sandbox_refund_reversal"

	// game wallet holds (see Hold below) — buy-in reservation, full refund, and
	// final-stack cash-out for skill-game (poker/dominó) integration.
	EntryGameHoldDebit     = "game_hold_debit"     // buy-in reservation
	EntryGameHoldRelease   = "game_hold_release"   // full refund, table/hand aborted before play
	EntryGameCashoutCredit = "game_cashout_credit" // final stack credited back on leaving the table
)

// Hold statuses.
const (
	HoldHeld     = "held"
	HoldReleased = "released"
	HoldSettled  = "settled" // consumed by a cash-out credit
)

// PIX deposit statuses.
const (
	DepositPending       = "pending"
	DepositConfirmed     = "confirmed"
	DepositRejectedCPF   = "rejected_cpf_mismatch" // legacy pre-saga state; reconciler migrates it to refund_pending
	DepositRefundPending = "refund_pending"
	DepositExpired       = "expired"
	DepositRefunded      = "refunded"      // Inter returned the payment to the payer (devolução)
	DepositRefundFailed  = "refund_failed" // devolução seen, but the wallet debit-back failed — needs manual reconciliation
)

// Withdrawal statuses.
const (
	WithdrawProcessing = "processing"
	WithdrawCompleted  = "completed"
	WithdrawReversed   = "reversed"
	WithdrawRefundFail = "refund_failed"
)

// DynamoDB table names (env-prefixed at the repository layer). Every table
// except `wallets` carries the `wallet_` segment so the wallet's tables never
// collide with ctech-dfe's or ctech-account's (e.g. `users`). `wallet_audit`
// already carries the prefix and is left unchanged.
const (
	TableWallets     = "wallets"
	TableLedger      = "wallet_ledger_entries"
	TableIdempotency = "wallet_idempotency"
	TablePixDeposits = "wallet_pix_deposits"
	TableWithdrawals = "wallet_withdrawals"
	TableUsers       = "wallet_users"
	TableAudit       = "wallet_audit"
	TableHolds       = "wallet_holds"

	// Asaas BaaS custody tables (docs/plans/2026-07-30-asaas-baas-implementation-plan.md
	// §2.4). Names are provider-neutral on purpose — Asaas is today's provider,
	// not a permanent commitment.
	TableBaasAccounts     = "wallet_baas_accounts"     // 1 row per user, custody lifecycle state
	TableTransferIntents  = "wallet_transfer_intents"  // §2.3 — transfer-authorization lookup
	TableSettlementLegs   = "wallet_settlement_legs"   // §6 netting batches
	TableMedReceivables   = "wallet_med_receivables"   // §7.3 clawback debt
	TableSandboxPurchases = "wallet_sandbox_purchases" // §9 — decoupled from wallet ledger tables on purpose
)

// DynamoDB GSI names.
const (
	GSIUser       = "gsi_user"        // wallets.user_id → both wallets of a user
	GSIIdem       = "gsi_idem"        // ledger_entries.idempotency_key → replay lookup
	GSIStatus     = "gsi_status"      // withdrawals.status → reconciliation scan; deposits.status → pending sweep
	GSIHoldStatus = "gsi_hold_status" // holds.status → stale-hold reconciliation scan

	GSIDepositProviderQR     = "gsi_deposit_provider_qr"     // wallet_pix_deposits.provider_qr_code_id → Asaas payment webhook resolution (plan §4.3)
	GSIBaasAccountID         = "gsi_baas_account_id"         // wallet_baas_accounts.provider_account_id → webhook resolution
	GSIBaasStatus            = "gsi_baas_status"             // wallet_baas_accounts.status → conservation-check sweep (approved accounts only)
	GSIIntentStatus          = "gsi_intent_status"           // wallet_transfer_intents.status → convergence/reconcile scan
	GSIBatchStatus           = "gsi_batch_status"            // wallet_settlement_legs.status → drift/convergence scan
	GSIMedStatus             = "gsi_med_status"              // wallet_med_receivables.status → open-debt scan, blocks withdrawal
	GSISandboxPurchaseStatus = "gsi_sandbox_purchase_status" // wallet_sandbox_purchases.status → pending sweep

	// GSISandboxPurchaseWebhookStatus backs the M2M webhook-notify-back retry
	// sweep (plan: M2M sandbox-purchase integration) — deliberately a second
	// GSI on the same table rather than overloading GSISandboxPurchaseStatus,
	// since "confirmed but webhook not yet delivered" and "pending payment"
	// are unrelated work queues.
	GSISandboxPurchaseWebhookStatus = "gsi_sandbox_purchase_webhook_status"
)

// IdemPrefix namespaces idempotency guard items in the idempotency table.
const IdemPrefix = "IDEM#"

// WalletPrefix namespaces a wallet's partition key (pk) in the wallets and
// ledger tables, so wallet records never collide with the (user_id, type)
// marker rows (USER#...) that share the wallets table. Mirrors the USER# marker.
const WalletPrefix = "WALLET#"

// MaxInboundReais is the absolute ceiling (in reais) on a single INBOUND
// money operation: a PIX deposit or a real→game fund. It is a hard cap no
// per-wallet override (MinDeposit/MaxDeposit, fee fields) may exceed — set
// directly in domain/wallet so every inbound path enforces the same number.
// Stored as centavos in MaxInboundAmount.
const (
	MaxInboundAmount = rpccontract.MaxAmountCents // centavos, shared with ui (B18)
	MaxInboundReais  = MaxInboundAmount / 100
)

// DefaultReceiptsPerMonth is the fallback PIX-receipt allowance per subaccount
// per calendar month, used when no configured value is wired in. The provider
// gives 100 free receipts a month and bills each one after that; the gap is
// deliberate headroom for charges opened but not yet paid (see
// WalletService.requireReceiptAllowance).
const DefaultReceiptsPerMonth = 95

// SandboxCreditsPerCent is the fixed conversion applied when real money is
// turned into sandbox credits (game → sandbox). R$ 1,00 (100 centavos) becomes
// 1000 credits, so this is 10 credits per centavo. The rate is a backend
// constant — never client-supplied — and is applied to the full real amount
// debited from `game`. Defined once in rpc-contract (money.json, shared with
// the ui — B18).
const SandboxCreditsPerCent = rpccontract.SandboxCreditsPerCent

// ToSandboxCredits converts a real-money amount in centavos into the number of
// sandbox credits it buys at the fixed rate.
func ToSandboxCredits(centavos int64) int64 {
	return centavos * SandboxCreditsPerCent
}

// Wallet is the authoritative balance record. Balance is integer centavos for
// `real` and `game`; for `sandbox` it is integer CREDITS (a virtual unit with no
// monetary value, converted from real money at SandboxCreditsPerCent). The two
// units never mix within one wallet.
//
// MinDeposit/MaxDeposit are OPTIONAL per-wallet PIX deposit-range overrides. All
// are set ONLY by an admin editing the item directly in DynamoDB — there is no
// API write path. Any unset (zero) field falls back to the package default. The
// effective minimum deposit never below AbsoluteMinDeposit, regardless of overrides.
type Wallet struct {
	WalletID   string `dynamodbav:"pk" json:"wallet_id"`
	UserID     string `dynamodbav:"user_id" json:"user_id"`
	Type       string `dynamodbav:"type" json:"type"`
	Balance    int64  `dynamodbav:"balance" json:"balance"`
	Version    int64  `dynamodbav:"version" json:"version"`
	MinDeposit int64  `dynamodbav:"min_deposit,omitempty" json:"min_deposit,omitempty"`
	MaxDeposit int64  `dynamodbav:"max_deposit,omitempty" json:"max_deposit,omitempty"`
	// CustodyEnabled is the admin-only production rollout allowlist for a real
	// wallet. When true (and the fleet capability is enabled), its PIX custody
	// rail is Asaas; false keeps the established Inter rail.
	CustodyEnabled bool `dynamodbav:"custody_enabled,omitempty" json:"-"`
	// MinWithdrawal is the OPTIONAL per-wallet withdrawal-amount floor override
	// (plan §5.2) — admin-only, same convention as MinDeposit above.
	MinWithdrawal int64  `dynamodbav:"min_withdrawal,omitempty" json:"min_withdrawal,omitempty"`
	CreatedAt     string `dynamodbav:"created_at" json:"created_at"`
	UpdatedAt     string `dynamodbav:"updated_at" json:"updated_at"`
}

// DescriptionMaxLen caps the optional free-form Description carried by ledger
// entries and purchases. Display metadata only — see LedgerEntry.Description.
const DescriptionMaxLen = 255

// LedgerEntry is an immutable audit row. balance_after is advisory; the
// authoritative balance is always Wallet.Balance.
//
// Amount is signed and its unit matches the owning wallet: centavos for `real`
// and `game`, credits for `sandbox` (e.g. a sandbox_purchase credit entry carries
// the credited credit amount, not the debited centavos).
type LedgerEntry struct {
	WalletID       string `dynamodbav:"pk" json:"wallet_id"`
	SK             string `dynamodbav:"sk" json:"-"`
	EntryID        string `dynamodbav:"entry_id" json:"entry_id"`
	Type           string `dynamodbav:"type" json:"type"`
	Amount         int64  `dynamodbav:"amount" json:"amount"` // signed; unit = owning wallet (centavos for real/game, credits for sandbox)
	BalanceAfter   int64  `dynamodbav:"balance_after" json:"balance_after"`
	IdempotencyKey string `dynamodbav:"idempotency_key" json:"-"`
	Ref            string `dynamodbav:"ref" json:"ref,omitempty"`
	// Description is free-form human-readable text supplied by the calling
	// service ("Recompensa diária", "Assinatura Plus — agosto/2026"). Display
	// metadata only: never parsed, never part of the idempotency request hash,
	// never authority for any amount. Capped at DescriptionMaxLen.
	Description string `dynamodbav:"description,omitempty" json:"description,omitempty"`
	CreatedAt   string `dynamodbav:"created_at" json:"created_at"`
}

// PixDeposit tracks an immediate PIX charge (cob) awaiting payment.
type PixDeposit struct {
	Txid           string `dynamodbav:"pk" json:"txid"`
	WalletID       string `dynamodbav:"wallet_id" json:"wallet_id"`
	UserID         string `dynamodbav:"user_id" json:"user_id"`
	AmountExpected int64  `dynamodbav:"amount_expected" json:"amount_expected"`
	Status         string `dynamodbav:"status" json:"status"`
	E2EID          string `dynamodbav:"e2e_id" json:"e2e_id,omitempty"`
	// PayerCPF/PayerName come only from the webhook body (Inter's charge re-query
	// no longer returns the payer) — persisted on first sight so the CPF-match
	// check, and any later manual reconciliation, has it even under a retry that
	// omits them. PayerCPF may be partially masked by Inter (e.g. "***137303**").
	PayerCPF  string `dynamodbav:"payer_cpf,omitempty" json:"payer_cpf,omitempty"`
	PayerName string `dynamodbav:"payer_name,omitempty" json:"payer_name,omitempty"`
	// Provider names which PIX rail opened this charge — "" (the default,
	// meaning Inter) for every deposit before Asaas custody existed, and for
	// every deposit made by a non-custodied user afterward. ProviderQRCodeID is
	// the Asaas-side handle the deposit-confirmation webhook resolves
	// payment.pixQrCodeId → txid through (plan §4.2, §4.3) — meaningless when
	// Provider is empty.
	Provider         string `dynamodbav:"provider,omitempty" json:"-"`
	ProviderQRCodeID string `dynamodbav:"provider_qr_code_id,omitempty" json:"-"`
	// QRCodePayload/QRCodeImage are the copyable PIX string and its rendered
	// image, stored at creation so a client that asks again — a refresh, a
	// retried POST — gets its charge back without a provider call. That matters
	// because a static QR has no payment record at the provider until somebody
	// actually pays it: re-querying an unpaid deposit finds nothing. Public
	// payable data, never a secret.
	QRCodePayload string `dynamodbav:"qr_code_payload,omitempty" json:"-"`
	QRCodeImage   string `dynamodbav:"qr_code_image,omitempty" json:"-"`
	// ProviderPaymentID is the immutable Asaas payment ID learned from its
	// webhook. It is required to re-query the payment and its linked customer.
	ProviderPaymentID string `dynamodbav:"provider_payment_id,omitempty" json:"-"`
	CreatedAt         string `dynamodbav:"created_at" json:"created_at"`
	TTL               int64  `dynamodbav:"expires_at" json:"-"` // business expiry; retained for durable idempotency/audit
}

// ProviderAsaas marks a PixDeposit opened against a user's Asaas subaccount
// rather than Inter's pooled account (plan §4.2). There is no ProviderInter
// constant — the empty string is Inter, matching every pre-migration row.
const ProviderAsaas = "asaas"

// Withdrawal tracks a PIX payout; the processing state is resolved by the
// reconciliation job so money is never left in limbo.
type Withdrawal struct {
	WithdrawalID   string `dynamodbav:"pk" json:"withdrawal_id"`
	WalletID       string `dynamodbav:"wallet_id" json:"wallet_id"`
	UserID         string `dynamodbav:"user_id" json:"user_id"`
	Amount         int64  `dynamodbav:"amount" json:"amount"`
	PixKey         string `dynamodbav:"pix_key" json:"pix_key"`
	Provider       string `dynamodbav:"provider,omitempty" json:"provider,omitempty"`
	Status         string `dynamodbav:"status" json:"status"`
	E2EID          string `dynamodbav:"e2e_id" json:"e2e_id,omitempty"`
	IdempotencyKey string `dynamodbav:"idempotency_key" json:"-"`
	CreatedAt      string `dynamodbav:"created_at" json:"created_at"`
	UpdatedAt      string `dynamodbav:"updated_at" json:"updated_at"`
}

// Hold is an open reservation against a player's game wallet, created at
// buy-in. It never bounds the eventual cash-out amount — the calling skill
// game's own table ledger is authoritative for how much a player's stack is
// worth when they leave; this record exists for idempotency, audit, and
// stale-hold detection (see the stale-hold reconciliation sweep).
type Hold struct {
	HoldID         string `dynamodbav:"pk" json:"hold_id"`
	WalletID       string `dynamodbav:"wallet_id" json:"wallet_id"`
	UserID         string `dynamodbav:"user_id" json:"user_id"`
	Amount         int64  `dynamodbav:"amount" json:"amount"`       // original reservation, centavos
	TableRef       string `dynamodbav:"table_ref" json:"table_ref"` // opaque caller reference (e.g. table_id:seat)
	Status         string `dynamodbav:"status" json:"status"`
	IdempotencyKey string `dynamodbav:"idempotency_key" json:"-"`
	CreatedAt      string `dynamodbav:"created_at" json:"created_at"`
	UpdatedAt      string `dynamodbav:"updated_at" json:"updated_at"`
}

// BaaS custody lifecycle states (wallet_baas_accounts.status). See
// docs/plans/2026-07-30-asaas-baas-implementation-plan.md §2.4, §3, §7.
// BaasFeePending/BaasFeePaid front the sequence: the provider charges a
// non-refundable verification fee per subaccount at creation, so the user pays
// it to CTech first and the subaccount is only opened once that charge clears
// (docs/specs/2026-08-30-asaas-only-deposits.md).
const (
	BaasFeePending       = "fee_pending"
	BaasFeePaid          = "fee_paid"
	BaasOnboarding       = "onboarding"
	BaasPendingDocuments = "pending_documents"
	BaasPendingApproval  = "pending_approval"
	BaasApproved         = "approved"
	BaasFrozen           = "frozen"
	BaasClosing          = "closing"
	BaasSubaccountClosed = "subaccount_closed"
	BaasClosed           = "closed"
)

// BaasAccount is a user's Asaas custody lifecycle record — deliberately its
// own table, not a field bag on Wallet: the real/game/sandbox rows in
// `wallets` stay exactly as they are pre-migration (plan §2.4). ProviderID/
// ProviderWalletID are named generically (Asaas's account.id/walletId today)
// because a future provider's IDs land in the same columns.
//
// APIKeyCiphertext/APIKeyNonce hold the subaccount's Asaas API key, AES-256-GCM
// encrypted under the single fleet-wide master key fetched once from SSM at
// startup (plan §3.3) — never plaintext at rest.
type BaasAccount struct {
	UserID            string `dynamodbav:"pk" json:"user_id"`
	Status            string `dynamodbav:"status" json:"status"`
	ProviderAccountID string `dynamodbav:"provider_account_id,omitempty" json:"provider_account_id,omitempty"`
	ProviderWalletID  string `dynamodbav:"provider_wallet_id,omitempty" json:"provider_wallet_id,omitempty"`
	APIKeyCiphertext  []byte `dynamodbav:"api_key_ciphertext,omitempty" json:"-"`
	APIKeyNonce       []byte `dynamodbav:"api_key_nonce,omitempty" json:"-"`
	EVPPixKey         string `dynamodbav:"evp_pix_key,omitempty" json:"-"` // created once, ever, per subaccount (plan §3.2 step 6)
	// ConservationDrift is Invariant #13's fail-closed kill-switch (plan §6):
	// set by the reconcile job's conservation-check sweep when this user's
	// Asaas subaccount balance stops matching real.Balance + game.Balance +
	// open game holds. While true, HoldGame and Withdraw refuse this user with
	// AccountBlocked rather than acting on data that may no longer be trustworthy
	// — cleared only by ops, once the drift is manually reconciled and explained.
	ConservationDrift bool `dynamodbav:"conservation_drift,omitempty" json:"-"`
	// FeePurchaseID links the verification-fee charge this onboarding is waiting
	// on. Set once, when onboarding opens; kept afterwards as the audit trail of
	// what the user paid for. The fee is never refunded (the provider consumes
	// it at subaccount creation and a rejected subaccount does not give it
	// back), so a rejected account is re-submitted, never re-charged.
	FeePurchaseID string `dynamodbav:"fee_purchase_id,omitempty" json:"-"`
	// FeeQRPayload/FeeQRImage are the verification fee's copyable PIX string and
	// its rendered image, stored when the charge is opened so the onboarding
	// screen can show it again on a reload without opening a second QR code at
	// the provider. Public payable data, never a secret — the same reasoning as
	// PixDeposit.QRCodePayload.
	FeeQRPayload string `dynamodbav:"fee_qr_payload,omitempty" json:"-"`
	FeeQRImage   string `dynamodbav:"fee_qr_image,omitempty" json:"-"`
	// PendingDocuments names what the provider is still waiting on, in its own
	// words. Kept alongside OnboardingURL rather than instead of it: the
	// provider returns requirements with no link, and "under review" with no
	// list is a dead end for the user.
	PendingDocuments []string `dynamodbav:"pending_documents,omitempty" json:"-"`
	// OnboardingURL is the provider-hosted document upload link, when the
	// provider says a pending document must be sent that way. Never a document
	// we relay ourselves: uploading a document that carries this URL through the
	// API is rejected by the provider.
	OnboardingURL string `dynamodbav:"onboarding_url,omitempty" json:"-"`
	// IncomeValue is the declared monthly income the provider requires on the
	// subaccount. Captured when onboarding is requested and used when the
	// subaccount is finally created, which is a separate step (the fee clears
	// in between).
	IncomeValue int64 `dynamodbav:"income_value,omitempty" json:"-"`
	// ReceiptsMonthKey/ReceiptsCount meter PIX receipts against the provider's
	// monthly free allowance, in the same window-key shape as
	// GameDepositCounters: a key that is not the current month means the window
	// rolled and the count is logically zero.
	ReceiptsMonthKey string `dynamodbav:"receipts_month_key,omitempty" json:"-"`
	ReceiptsCount    int64  `dynamodbav:"receipts_count,omitempty" json:"-"`
	CreatedAt        string `dynamodbav:"created_at" json:"created_at"`
	UpdatedAt        string `dynamodbav:"updated_at" json:"updated_at"`
}

// ReceiptsUsed reports how many PIX receipts this subaccount has taken in
// monthKey. A stale window key counts as zero — the counter is never reset by a
// writer, it is simply superseded when the month rolls.
func (a *BaasAccount) ReceiptsUsed(monthKey string) int64 {
	if a == nil || a.ReceiptsMonthKey != monthKey {
		return 0
	}
	return a.ReceiptsCount
}

// Transfer-intent statuses (wallet_transfer_intents.status). See plan §2.3.
const (
	IntentAwaitingAuthorization = "awaiting_authorization"
	IntentProcessing            = "processing"
	IntentDone                  = "done"
	IntentFailed                = "failed"
	IntentSuperseded            = "superseded" // §9.1a — reversed before the forward leg ever reached done
	IntentCancelled             = "cancelled"  // Asaas auto-cancelled after 3 failed authorization responses
)

const (
	TransferDestinationPIX    = "pix"
	TransferDestinationWallet = "wallet"
)

// Transfer-intent kinds — what CreateTransfer call this row is tracking.
const (
	IntentKindWithdrawalPayout       = "withdrawal_payout"
	IntentKindSettlementLeg          = "settlement_leg"
	IntentKindSandboxPurchaseSettle  = "sandbox_purchase_settlement" // §9.1a forward leg
	IntentKindSandboxPurchaseReverse = "sandbox_purchase_reversal"   // §9.1a reversal leg
)

// Direct PIX→sandbox purchase statuses (plan §9.3). Deliberately its own
// status set, not DepositPending/Confirmed — this is a sale, not custody.
const (
	SandboxPurchasePending       = "pending"
	SandboxPurchaseConfirmed     = "confirmed"
	SandboxPurchaseRefundPending = "refund_pending"
	SandboxPurchaseRefunded      = "refunded"
	SandboxPurchaseExpired       = "expired"
)

// M2M webhook notify-back delivery status (RequestingClient-owned purchases
// only — empty/unset for purchases the user opened directly, since there is
// no webhook to deliver). WebhookFailed is what GSISandboxPurchaseWebhookStatus
// scans for the retry sweep; delivered/never-applicable rows fall out of it.
const (
	WebhookDelivered = "delivered"
	WebhookFailed    = "failed"
)

// SandboxPurchase tracks a direct PIX→sandbox-credits sale (plan §9.1/§9.3) —
// its own table (TableSandboxPurchases), deliberately separate from
// wallet_pix_deposits: a deposit is custody (money becomes the user's, held
// for them); this is a sale (money becomes CTech's, immediately). CreditSK is
// the sandbox ledger entry key the purchase's credit landed at, so §9.2's
// eligibility check (AnyDebitSince) has something to compare against. E2EID
// is populated once the webhook/re-query reports it, since Inter's Refund is
// keyed by e2eID, not by charge/purchase ID (same as every other Inter refund
// call site in this codebase).
type SandboxPurchase struct {
	PurchaseID     string `dynamodbav:"pk" json:"purchase_id"`
	UserID         string `dynamodbav:"user_id" json:"user_id"`
	SKU            string `dynamodbav:"sku" json:"sku"`
	AmountExpected int64  `dynamodbav:"amount_expected" json:"amount_expected"` // centavos, the PIX price
	CreditsGranted int64  `dynamodbav:"credits_granted" json:"credits_granted"` // sandbox credits
	RequestHash    string `dynamodbav:"request_hash" json:"-"`
	Status         string `dynamodbav:"status" json:"status"`
	CreditSK       string `dynamodbav:"credit_sk,omitempty" json:"-"`
	E2EID          string `dynamodbav:"e2e_id,omitempty" json:"e2e_id,omitempty"`
	// Description mirrors LedgerEntry.Description: optional caller-supplied
	// display text, never authority for price or credits.
	Description string `dynamodbav:"description,omitempty" json:"description,omitempty"`
	// RequestingClient is the AZP (M2M client_id) of the caller that opened
	// this purchase on a user's behalf via the M2M route — empty when the
	// user opened it directly. Owns two things: webhook notify-back routing
	// (looked up in the M2M client registry by this value) and cross-client
	// isolation (a client may only read/refund purchases it created).
	RequestingClient string `dynamodbav:"requesting_client,omitempty" json:"-"`
	// WebhookStatus tracks delivery of the async notify-back to
	// RequestingClient's registered webhook URL — empty until first attempted,
	// WebhookFailed/WebhookDelivered after. Always empty for user-direct
	// purchases (RequestingClient empty), since there is nothing to notify.
	WebhookStatus string `dynamodbav:"webhook_status,omitempty" json:"-"`
	CreatedAt     string `dynamodbav:"created_at" json:"created_at"`
	UpdatedAt     string `dynamodbav:"updated_at" json:"updated_at"`
	TTL           int64  `dynamodbav:"expires_at,omitempty" json:"-"` // business expiry; row is retained for idempotency
}

const TableProductPurchases = "wallet_product_purchases"

const (
	GSIProductPurchaseStatus        = "gsi_product_purchase_status"
	GSIProductPurchaseWebhookStatus = "gsi_product_purchase_webhook_status"
)

const (
	ProductPurchasePending   = "pending"
	ProductPurchaseConfirmed = "confirmed"
	ProductPurchaseRefunded  = "refunded"
)

// Kinds of sale that share this row.
//
// The row is shared deliberately: a caller-supplied-amount charge differs from a
// catalogue sale in exactly one field — where the amount comes from — and every
// other thing about it (the reservation, the txid, the confirm-by-re-query, the
// refund, the sweep) is the same machinery. A second table would be a second
// copy of all of that, and the copy is what drifts.
//
// The kind is what the two are told apart by, in the notify-back and in a log.
// Not the SKU namespace: for a charge, that field holds a label the caller owns,
// and inferring wallet's own routing from a string billing chose is exactly the
// coupling this constant exists to avoid.
const (
	ProductPurchaseKindProduct = "product"
	ProductPurchaseKindCharge  = "charge"
	// ProductPurchaseKindCustodyFee is the one-off fee a user pays to have their
	// custody subaccount opened. A sale like any other here, with two traits the
	// others do not share: it is collected at the custody provider rather than
	// at Inter, and it is never refunded (see services/custody_fee.go).
	ProductPurchaseKindCustodyFee = "custody_fee"
)

// ProductPurchase mirrors SandboxPurchase's shape minus everything about
// credits: no CreditSK, no CreditsGranted, no ledger entry type. There is no
// refund_pending status — a refund has nothing to resume except the PIX
// provider call itself, which is idempotent on E2EID
// (docs/specs/2026-08-12-product-purchase-skus.md).
type ProductPurchase struct {
	PurchaseID string `dynamodbav:"pk" json:"purchase_id"`
	UserID     string `dynamodbav:"user_id" json:"user_id"`
	// SKU is the catalogue id for a product sale and the caller's own opaque
	// reference for a charge (an invoice id, for ctech-billing). One attribute
	// rather than two because it is one thing — what this sale was for — and a
	// nullable second column would make every read ask which one to look at.
	SKU string `dynamodbav:"sku" json:"sku"`
	// Kind is ProductPurchaseKindProduct when absent, which is what every row
	// written before charges existed is.
	Kind           string `dynamodbav:"kind,omitempty" json:"kind,omitempty"`
	AmountExpected int64  `dynamodbav:"amount_expected" json:"amount_expected"`
	RequestHash    string `dynamodbav:"request_hash" json:"-"`
	Status         string `dynamodbav:"status" json:"status"`
	E2EID          string `dynamodbav:"e2e_id,omitempty" json:"e2e_id,omitempty"`
	// Description mirrors LedgerEntry.Description: optional caller-supplied
	// display text, never authority for the amount.
	Description      string `dynamodbav:"description,omitempty" json:"description,omitempty"`
	RequestingClient string `dynamodbav:"requesting_client,omitempty" json:"-"`
	WebhookStatus    string `dynamodbav:"webhook_status,omitempty" json:"-"`
	CreatedAt        string `dynamodbav:"created_at" json:"created_at"`
	UpdatedAt        string `dynamodbav:"updated_at" json:"updated_at"`
	TTL              int64  `dynamodbav:"expires_at,omitempty" json:"-"`
}

// MED receivable statuses (plan §7.3).
const (
	MedReceivableOpen    = "open"
	MedReceivableSettled = "settled"
)

// MedReceivable is a debt record created when a MED (Mecanismo Especial de
// Devolução) clawback exceeds what's currently in the wallet — never a
// negative balance (Invariant #1 stays literal), a separate record instead
// (plan §7.3). Deliberately excluded from Invariant #13's conservation-check
// "everything is explained" set on its own line: a receivable is not custody
// drift, and conflating the two would hide a real drift behind a legitimate
// receivable or vice versa.
type MedReceivable struct {
	ReceivableID string `dynamodbav:"pk" json:"receivable_id"`
	UserID       string `dynamodbav:"user_id" json:"user_id"`
	WalletID     string `dynamodbav:"wallet_id" json:"wallet_id"`
	Amount       int64  `dynamodbav:"amount" json:"amount"` // outstanding centavos
	Status       string `dynamodbav:"status" json:"status"`
	Ref          string `dynamodbav:"ref,omitempty" json:"ref,omitempty"` // Asaas MED event id
	CreatedAt    string `dynamodbav:"created_at" json:"created_at"`
	UpdatedAt    string `dynamodbav:"updated_at" json:"updated_at"`
}

// TransferIntent records what a CreateTransfer call SHOULD be, before it is
// ever sent — the transfer-authorization webhook (plan §2.3) does one GetItem
// by ExternalReference and compares the callback's amount/destination against
// this row, approving only on an exact match. This is the single choke point
// that catches a transfer built for the wrong amount/destination before Asaas
// ever moves money.
type TransferIntent struct {
	ExternalReference string `dynamodbav:"pk" json:"external_reference"`
	Kind              string `dynamodbav:"kind" json:"kind"`
	Status            string `dynamodbav:"status" json:"status"`
	UserID            string `dynamodbav:"user_id" json:"user_id"`
	Amount            int64  `dynamodbav:"amount" json:"amount"`
	// Destination is whichever identifier the transfer targets — a PIX key
	// (withdrawal payout, plan §5.2 leg 1) or an Asaas walletId (fee sweep,
	// settlement leg, sandbox-purchase settlement/reversal) — compared
	// verbatim against the authorization webhook's payload (plan §2.3 step 2).
	Destination        string `dynamodbav:"destination,omitempty" json:"destination,omitempty"`
	DestinationType    string `dynamodbav:"destination_type,omitempty" json:"destination_type,omitempty"`
	Ref                string `dynamodbav:"ref,omitempty" json:"ref,omitempty"` // e.g. withdrawal ID, batch leg, ledger credit SK (§9.1a)
	ProviderTransferID string `dynamodbav:"provider_transfer_id,omitempty" json:"provider_transfer_id,omitempty"`
	TransferFee        int64  `dynamodbav:"transfer_fee,omitempty" json:"transfer_fee,omitempty"` // §5.2 — Asaas's own leg-1 fee, read back from the response
	CreatedAt          string `dynamodbav:"created_at" json:"created_at"`
	UpdatedAt          string `dynamodbav:"updated_at" json:"updated_at"`
}
