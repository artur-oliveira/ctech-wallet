// Wallet API contract types (mirror api/internal/api/v1 responses).

// `game` holds REAL money ring-fenced for games: withdrawable via `real`, and the
// only route by which real money reaches a game or sandbox. `sandbox` is virtual
// and has no monetary value.
export type WalletType = 'real' | 'game' | 'sandbox'

export interface Wallet {
    wallet_id: string
    user_id: string
    type: WalletType
    balance: number // integer centavos
    version: number
    fee_bps?: number
    fee_min?: number
    fee_max?: number
    min_deposit?: number
    max_deposit?: number
    created_at: string
    updated_at: string
}

// game and sandbox are ABSENT until the user activates gambling. Their absence is
// the signal — not a separate boolean that could drift out of sync with reality.
// `activated` mirrors it for readability at the call site.
export interface Balances {
    real: Wallet
    activated: boolean
    game?: Wallet
    sandbox?: Wallet
}

/** The ledger pair written by any transfer between two of the caller's wallets.
 *  `credit` is omitted on an idempotent replay (the server returns only the prior
 *  debit entry), so consumers must not assume it is always present. */
export interface Transfer {
    debit: LedgerEntry
    credit?: LedgerEntry
}

export interface DepositResult {
    txid: string
    amount: number
    status: string
    pix_copia_e_cola: string
    qr_code_base64?: string
    expires_at: number // unix seconds — when the charge stops being payable
}

export interface Withdrawal {
    withdrawal_id: string
    wallet_id: string
    user_id: string
    amount: number
    fee: number
    pix_key: string
    status: 'processing' | 'completed' | 'reversed' | 'refund_failed'
    e2e_id?: string
    created_at: string
    updated_at: string
}

export interface LedgerEntry {
    entry_id: string
    wallet_id: string
    type: string
    amount: number // signed; unit matches the owning wallet (centavos for real/game, credits for sandbox)
    balance_after: number
    ref?: string
    created_at: string
}

export interface LedgerPage {
    items: LedgerEntry[]
    next_cursor: string | null
    has_next: boolean
}

/** Name-related profile claims decoded from the OIDC id_token. */
export interface Profile {
    username?: string
    first_name?: string
    last_name?: string
}

/**
 * Wallet-side caller state. `terms_addendum_accepted` is computed server-side
 * against the current version constant — bumping the version re-gates the user.
 */
export interface MeResponse {
    user_id: string
    terms_addendum_accepted: boolean
    terms_addendum_version: string
}

export interface PendingGameLimits {
    daily: number
    weekly: number
    monthly: number
    applies_at: string
}

export interface GameLimits {
    daily: number
    weekly: number
    monthly: number
    pending?: PendingGameLimits
}

export interface SelfExclusion {
    period: '30d' | '90d' | 'indefinite'
    requested_at: string
    until?: string
}

export interface GameLimitsStatus {
    limits: GameLimits | null
    usage: {
        daily: number
        weekly: number
        monthly: number
        day_resets_at: string
        week_resets_at: string
        month_resets_at: string
    }
    excluded?: SelfExclusion
}

export interface GameLimitsInput {
    daily_limit: number
    weekly_limit: number
    monthly_limit: number
}
