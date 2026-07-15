// Package wallet holds the wallet domain model: the two balance types, ledger
// entry kinds, deposit/withdrawal statuses, table/index names, and money math.
// Every string and numeric key lives here as a named constant.
package wallet

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
	EntryFee             = "fee"
	EntryGameDebit       = "game_debit"
	EntryGameCredit      = "game_credit"
	EntrySandboxPurchase = "sandbox_purchase"
	EntrySandboxCredit   = "sandbox_credit"
	EntryReversal        = "reversal" // credit-back of a failed withdrawal

	// Ring-fence transfers between `real` and `game`. Funding is metered by the
	// personal limit engine; returning is always free and never limited.
	EntryGameFundDebit    = "game_fund_debit"    // debit real
	EntryGameFundCredit   = "game_fund_credit"   // credit game
	EntryGameReturnDebit  = "game_return_debit"  // debit game
	EntryGameReturnCredit = "game_return_credit" // credit real
)

// PIX deposit statuses.
const (
	DepositPending     = "pending"
	DepositConfirmed   = "confirmed"
	DepositRejectedCPF = "rejected_cpf_mismatch"
	DepositExpired     = "expired"
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
)

// DynamoDB GSI names.
const (
	GSIUser   = "gsi_user"   // wallets.user_id → both wallets of a user
	GSIIdem   = "gsi_idem"   // ledger_entries.idempotency_key → replay lookup
	GSIStatus = "gsi_status" // withdrawals.status → reconciliation scan
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
	MaxInboundReais  = 1_000_000
	MaxInboundAmount = MaxInboundReais * 100 // centavos
)

// Wallet is the authoritative balance record. Balance is integer centavos.
//
// FeeBps/FeeMin/FeeMax are OPTIONAL per-wallet withdrawal-fee overrides, and
// MinDeposit/MaxDeposit are OPTIONAL per-wallet PIX deposit-range overrides. All
// are set ONLY by an admin editing the item directly in DynamoDB — there is no
// API write path. Any unset (zero) field falls back to the package default. The
// effective fee can never drop below AbsoluteFeeMin, and the effective minimum
// deposit never below AbsoluteMinDeposit, regardless of overrides.
type Wallet struct {
	WalletID   string `dynamodbav:"pk" json:"wallet_id"`
	UserID     string `dynamodbav:"user_id" json:"user_id"`
	Type       string `dynamodbav:"type" json:"type"`
	Balance    int64  `dynamodbav:"balance" json:"balance"`
	Version    int64  `dynamodbav:"version" json:"version"`
	FeeBps     int64  `dynamodbav:"fee_bps,omitempty" json:"fee_bps,omitempty"`
	FeeMin     int64  `dynamodbav:"fee_min,omitempty" json:"fee_min,omitempty"`
	FeeMax     int64  `dynamodbav:"fee_max,omitempty" json:"fee_max,omitempty"`
	MinDeposit int64  `dynamodbav:"min_deposit,omitempty" json:"min_deposit,omitempty"`
	MaxDeposit int64  `dynamodbav:"max_deposit,omitempty" json:"max_deposit,omitempty"`
	CreatedAt  string `dynamodbav:"created_at" json:"created_at"`
	UpdatedAt  string `dynamodbav:"updated_at" json:"updated_at"`
}

// LedgerEntry is an immutable audit row. balance_after is advisory; the
// authoritative balance is always Wallet.Balance.
type LedgerEntry struct {
	WalletID       string `dynamodbav:"pk" json:"wallet_id"`
	SK             string `dynamodbav:"sk" json:"-"`
	EntryID        string `dynamodbav:"entry_id" json:"entry_id"`
	Type           string `dynamodbav:"type" json:"type"`
	Amount         int64  `dynamodbav:"amount" json:"amount"` // signed centavos
	BalanceAfter   int64  `dynamodbav:"balance_after" json:"balance_after"`
	IdempotencyKey string `dynamodbav:"idempotency_key" json:"-"`
	Ref            string `dynamodbav:"ref" json:"ref,omitempty"`
	CreatedAt      string `dynamodbav:"created_at" json:"created_at"`
}

// PixDeposit tracks an immediate PIX charge (cob) awaiting payment.
type PixDeposit struct {
	Txid           string `dynamodbav:"pk" json:"txid"`
	WalletID       string `dynamodbav:"wallet_id" json:"wallet_id"`
	UserID         string `dynamodbav:"user_id" json:"user_id"`
	AmountExpected int64  `dynamodbav:"amount_expected" json:"amount_expected"`
	Status         string `dynamodbav:"status" json:"status"`
	E2EID          string `dynamodbav:"e2e_id" json:"e2e_id,omitempty"`
	CreatedAt      string `dynamodbav:"created_at" json:"created_at"`
	TTL            int64  `dynamodbav:"ttl" json:"-"` // Dynamo TTL epoch, 15 min
}

// Withdrawal tracks a PIX payout; the processing state is resolved by the
// reconciliation job so money is never left in limbo.
type Withdrawal struct {
	WithdrawalID   string `dynamodbav:"pk" json:"withdrawal_id"`
	WalletID       string `dynamodbav:"wallet_id" json:"wallet_id"`
	UserID         string `dynamodbav:"user_id" json:"user_id"`
	Amount         int64  `dynamodbav:"amount" json:"amount"`
	Fee            int64  `dynamodbav:"fee" json:"fee"`
	PixKey         string `dynamodbav:"pix_key" json:"pix_key"`
	Status         string `dynamodbav:"status" json:"status"`
	E2EID          string `dynamodbav:"e2e_id" json:"e2e_id,omitempty"`
	IdempotencyKey string `dynamodbav:"idempotency_key" json:"-"`
	CreatedAt      string `dynamodbav:"created_at" json:"created_at"`
	UpdatedAt      string `dynamodbav:"updated_at" json:"updated_at"`
}
