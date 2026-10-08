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

	TableSandboxPurchases = "wallet_sandbox_purchases" // §9 — decoupled from wallet ledger tables on purpose
)

// DynamoDB GSI names.
const (
	GSIUser       = "gsi_user"        // wallets.user_id → both wallets of a user
	GSIIdem       = "gsi_idem"        // ledger_entries.idempotency_key → replay lookup
	GSIStatus     = "gsi_status"      // withdrawals.status → reconciliation scan; deposits.status → pending sweep
	GSIHoldStatus = "gsi_hold_status" // holds.status → stale-hold reconciliation scan

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
	// MinWithdrawal is the OPTIONAL per-wallet withdrawal-amount floor override
	// (plan §5.2) — admin-only, same convention as MinDeposit above.
	MinWithdrawal int64 `dynamodbav:"min_withdrawal,omitempty" json:"min_withdrawal,omitempty"`
	// Daily PIX limits (admin-only, same convention as MinDeposit/MaxDeposit).
	// Zero means "use the default" (EffectiveDailyLimits).
	DailyDepositCap    int64  `dynamodbav:"daily_deposit_cap,omitempty" json:"daily_deposit_cap,omitempty"`
	DailyWithdrawCap   int64  `dynamodbav:"daily_withdraw_cap,omitempty" json:"daily_withdraw_cap,omitempty"`
	DailyWithdrawCount int64  `dynamodbav:"daily_withdraw_count,omitempty" json:"daily_withdraw_count,omitempty"`
	CreatedAt          string `dynamodbav:"created_at" json:"created_at"`
	UpdatedAt          string `dynamodbav:"updated_at" json:"updated_at"`
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
	// QRCodePayload/QRCodeImage are the copyable PIX string and its rendered
	// image, stored at creation so a client that asks again — a refresh, a
	// retried POST — gets its charge back without a provider call. That matters
	// because a static QR has no payment record at the provider until somebody
	// actually pays it: re-querying an unpaid deposit finds nothing. Public
	// payable data, never a secret.
	QRCodePayload string `dynamodbav:"qr_code_payload,omitempty" json:"-"`
	QRCodeImage   string `dynamodbav:"qr_code_image,omitempty" json:"-"`
	CreatedAt     string `dynamodbav:"created_at" json:"created_at"`
	TTL           int64  `dynamodbav:"expires_at" json:"-"` // business expiry; retained for durable idempotency/audit
}

// Withdrawal tracks a PIX payout; the processing state is resolved by the
// reconciliation job so money is never left in limbo.
type Withdrawal struct {
	WithdrawalID   string `dynamodbav:"pk" json:"withdrawal_id"`
	WalletID       string `dynamodbav:"wallet_id" json:"wallet_id"`
	UserID         string `dynamodbav:"user_id" json:"user_id"`
	Amount         int64  `dynamodbav:"amount" json:"amount"`
	PixKey         string `dynamodbav:"pix_key" json:"pix_key"`
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
