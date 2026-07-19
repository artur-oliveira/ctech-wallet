// Temporary dev-only mock so the full app can be exercised without real OAuth.
// Gated by NEXT_PUBLIC_MOCK_AUTH. No production code path reads this unless the
// flag is set, so it is safe to leave in the tree (and harmless when off).
import type {
  Balances,
  DepositResult,
  GameLimits,
  GameLimitsInput,
  GameLimitsStatus,
  LedgerEntry,
  LedgerPage,
  MeResponse,
  Transfer,
  Wallet,
  WalletType,
  Withdrawal,
} from '@/lib/types/api'
import {withdrawalFee} from '@/lib/utils/fee'

export const USE_MOCK = process.env.NEXT_PUBLIC_MOCK_AUTH === 'true'

export const MOCK_PROFILE = {username: 'mock.user', first_name: 'Mock', last_name: 'User'}

function mk(type: WalletType, balance: number): Wallet {
  return {
    wallet_id: `w_${type}`,
    user_id: 'mock_user',
    type,
    balance,
    version: 1,
    fee_bps: 200,
    fee_min: 100,
    fee_max: 1000,
    min_deposit: 100,
    max_deposit: 100_000_000,
    created_at: '2026-01-01T00:00:00.000Z',
    updated_at: '2026-01-01T00:00:00.000Z',
  }
}

const state = {
  real: mk('real', 2_500_00),
  game: mk('game', 50_000),
  sandbox: mk('sandbox', 1200),
  activated: true,
  limits: {daily: 50_000, weekly: 200_000, monthly: 500_000} as GameLimits,
  excluded: undefined as GameLimitsStatus['excluded'],
  ledger: [
    {
      entry_id: 'e3',
      wallet_id: 'w_real',
      type: 'deposit',
      amount: 100_000,
      balance_after: 2_500_00,
      created_at: new Date(Date.now() - 86_400_000).toISOString()
    },
    {
      entry_id: 'e2',
      wallet_id: 'w_real',
      type: 'withdraw',
      amount: -5_000,
      balance_after: 1_500_00,
      created_at: new Date(Date.now() - 2 * 86_400_000).toISOString()
    },
    {
      entry_id: 'e1',
      wallet_id: 'w_real',
      type: 'game_debit',
      amount: -20_000,
      balance_after: 2_000_00,
      created_at: new Date(Date.now() - 3 * 86_400_000).toISOString()
    },
  ] as LedgerEntry[],
}

function addEntry(wallet: Wallet, type: string, amount: number): LedgerEntry {
  wallet.balance += amount
  const entry: LedgerEntry = {
    entry_id: `e_${Date.now()}_${Math.random().toString(36).slice(2, 6)}`,
    wallet_id: wallet.wallet_id,
    type,
    amount,
    balance_after: wallet.balance,
    created_at: new Date().toISOString(),
  }
  state.ledger.unshift(entry)
  return entry
}

/** In-memory stand-in for ApiClient. Mirrors the same method surface. */
export class MockApiClient {
  setToken(_token: string | null): void {
    /* no-op: mock never sends a real Authorization header */
    void _token
  }

  async me(): Promise<MeResponse> {
    return {user_id: 'mock_user', terms_addendum_accepted: true, terms_addendum_version: '1.0'}
  }

  async acceptTermsAddendum(): Promise<void> {
    /* no-op */
  }

  async getBalances(): Promise<Balances> {
    return {real: state.real, activated: state.activated, game: state.game, sandbox: state.sandbox}
  }

  async createDeposit(amount: number): Promise<DepositResult> {
    return {
      txid: `tx_${Date.now()}`,
      amount,
      status: 'pending',
      pix_copia_e_cola:
        '00020126580014BR.GOV.BCB.PIX0136mock@aoctech.app5204000053039865405' +
        '000.005802BR5913Mock User6009SAO PAULO62070503***6304MOCK',
      expires_at: Date.now() / 1000 + 300,
    }
  }

  async createWithdrawal(amount: number, _idempotencyKey: string): Promise<Withdrawal> {
    void _idempotencyKey
    const fee = withdrawalFee(amount, state.real)
    addEntry(state.real, 'withdraw', -amount)
    addEntry(state.real, 'fee', -fee)
    return {
      withdrawal_id: `wd_${Date.now()}`,
      wallet_id: state.real.wallet_id,
      user_id: 'mock_user',
      amount,
      fee,
      pix_key: '12345678901', // mock user's registered CPF — no client-supplied key anymore
      status: 'completed',
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString(),
    }
  }

  async purchaseSandbox(amount: number): Promise<Transfer> {
    const debit = addEntry(state.game, 'sandbox_purchase', -amount)
    const credit = addEntry(state.sandbox, 'sandbox_credit', amount)
    return {debit, credit}
  }

  async activateGambling(limits: GameLimitsInput): Promise<{ game: Wallet; sandbox: Wallet }> {
    state.activated = true
    state.limits = {daily: limits.daily_limit, weekly: limits.weekly_limit, monthly: limits.monthly_limit}
    return {game: state.game, sandbox: state.sandbox}
  }

  async getGameLimits(): Promise<GameLimitsStatus> {
    return {
      limits: state.limits,
      usage: {
        daily: 12_000,
        weekly: 35_000,
        monthly: 80_000,
        day_resets_at: new Date(Date.now() + 86_400_000).toISOString(),
        week_resets_at: new Date(Date.now() + 4 * 86_400_000).toISOString(),
        month_resets_at: new Date(Date.now() + 12 * 86_400_000).toISOString(),
      },
      excluded: state.excluded,
    }
  }

  async setGameLimits(input: GameLimitsInput): Promise<GameLimits> {
    state.limits = {daily: input.daily_limit, weekly: input.weekly_limit, monthly: input.monthly_limit}
    return state.limits
  }

  async cancelPendingGameLimits(): Promise<GameLimits> {
    state.limits = {...state.limits, pending: undefined}
    return state.limits
  }

  async selfExclude(period: '30d' | '90d' | 'indefinite'): Promise<void> {
    state.excluded = {
      period,
      requested_at: new Date().toISOString(),
      until: period === 'indefinite' ? undefined : new Date(Date.now() + (period === '30d' ? 30 : 90) * 86_400_000).toISOString(),
    }
  }

  async revokeSelfExclusion(): Promise<void> {
    state.excluded = undefined
  }

  async fundGame(amount: number): Promise<Transfer> {
    const debit = addEntry(state.real, 'game_fund_debit', -amount)
    const credit = addEntry(state.game, 'game_fund_credit', amount)
    return {debit, credit}
  }

  async returnFromGame(amount: number): Promise<Transfer> {
    const debit = addEntry(state.game, 'game_return_debit', -amount)
    const credit = addEntry(state.real, 'game_return_credit', amount)
    return {debit, credit}
  }

  async getLedger(type: WalletType): Promise<LedgerPage> {
    const items = state.ledger.filter((e) => e.wallet_id === `w_${type}`)
    return {items, next_cursor: null, has_next: false}
  }
}
