import type {DepositResult, LedgerEntry} from '@/lib/types/api'

export type TrackedTransactionStatus =
  | 'pending'
  | 'processing'
  | 'confirmed'
  | 'completed'
  | 'expired'
  | 'reversed'
  | 'refund_failed'

export interface TrackedTransaction {
  id: string
  kind: 'deposit' | 'withdrawal'
  amount: number
  fee?: number
  status: TrackedTransactionStatus
  created_at: string
  updated_at?: string
  expires_at?: number
}

export interface RealtimeTransactionStatus {
  type: 'deposit_confirmed' | 'withdraw_completed' | 'withdraw_reversed' | 'withdraw_refund_failed'
  transactionId: string
}

const MAX_TRACKED_TRANSACTIONS = 12
const TRACKED_STATUSES = new Set<TrackedTransactionStatus>([
  'pending',
  'processing',
  'confirmed',
  'completed',
  'expired',
  'reversed',
  'refund_failed',
])
const REALTIME_STATUS: Record<RealtimeTransactionStatus['type'], TrackedTransactionStatus> = {
  deposit_confirmed: 'confirmed',
  withdraw_completed: 'completed',
  withdraw_reversed: 'reversed',
  withdraw_refund_failed: 'refund_failed',
}

export function upsertTransaction(
  transactions: TrackedTransaction[],
  next: TrackedTransaction,
): TrackedTransaction[] {
  return [next, ...transactions.filter((item) => item.id !== next.id)]
    .sort((a, b) => b.created_at.localeCompare(a.created_at))
    .slice(0, MAX_TRACKED_TRANSACTIONS)
}

export function applyRealtimeStatus(
  transactions: TrackedTransaction[],
  event: RealtimeTransactionStatus,
): TrackedTransaction[] {
  const status = REALTIME_STATUS[event.type]
  return transactions.map((item) =>
    item.id === event.transactionId
      ? {...item, status, updated_at: new Date().toISOString()}
      : item,
  )
}

export function reconcileTransactionHistory(
  transactions: TrackedTransaction[],
  ledger: LedgerEntry[],
  nowMs: number,
): TrackedTransaction[] {
  const confirmedDeposits = new Set(
    ledger.filter((entry) => entry.type === 'deposit' && entry.ref).map((entry) => entry.ref),
  )
  const reversedWithdrawals = new Set(
    ledger
      .filter((entry) => entry.type === 'reversal' && entry.ref?.startsWith('reverse:'))
      .map((entry) => entry.ref?.slice('reverse:'.length)),
  )

  return transactions.map((item) => {
    if (item.kind === 'deposit' && item.status === 'pending') {
      if (confirmedDeposits.has(item.id)) return {...item, status: 'confirmed'}
      if (item.expires_at && item.expires_at * 1000 <= nowMs) return {...item, status: 'expired'}
    }
    if (item.kind === 'withdrawal' && item.status === 'processing' && reversedWithdrawals.has(item.id)) {
      return {...item, status: 'reversed'}
    }
    return item
  })
}

export function parseTransactionHistory(raw: string | null): TrackedTransaction[] {
  if (!raw) return []
  try {
    const value = JSON.parse(raw) as unknown
    if (!Array.isArray(value)) return []
    return value.filter((item): item is TrackedTransaction => {
      if (!item || typeof item !== 'object') return false
      const candidate = item as Partial<TrackedTransaction>
      return (
        typeof candidate.id === 'string'
        && candidate.id.length > 0
        && (candidate.kind === 'deposit' || candidate.kind === 'withdrawal')
        && typeof candidate.amount === 'number'
        && Number.isSafeInteger(candidate.amount)
        && candidate.amount > 0
        && (candidate.fee == null || (
          typeof candidate.fee === 'number'
          && Number.isSafeInteger(candidate.fee)
          && candidate.fee >= 0
        ))
        && (candidate.expires_at == null || (
          typeof candidate.expires_at === 'number'
          && Number.isSafeInteger(candidate.expires_at)
        ))
        && typeof candidate.status === 'string'
        && TRACKED_STATUSES.has(candidate.status as TrackedTransactionStatus)
        && typeof candidate.created_at === 'string'
        && !Number.isNaN(Date.parse(candidate.created_at))
      )
    }).slice(0, MAX_TRACKED_TRANSACTIONS)
  } catch {
    return []
  }
}

export function parseStoredDeposit(raw: string | null): DepositResult | null {
  if (!raw) return null
  try {
    const value = JSON.parse(raw) as Partial<DepositResult>
    if (
      typeof value.txid !== 'string'
      || value.txid.length === 0
      || typeof value.amount !== 'number'
      || !Number.isSafeInteger(value.amount)
      || value.amount <= 0
      || typeof value.status !== 'string'
      || typeof value.pix_copia_e_cola !== 'string'
      || value.pix_copia_e_cola.length === 0
      || typeof value.expires_at !== 'number'
      || !Number.isFinite(value.expires_at)
    ) return null
    if (value.qr_code_base64 != null && typeof value.qr_code_base64 !== 'string') return null
    return value as DepositResult
  } catch {
    return null
  }
}
