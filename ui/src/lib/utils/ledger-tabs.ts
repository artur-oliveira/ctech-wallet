import type {WalletType} from '@/lib/types/api'

export const LEDGER_TAB_KEYS = ['ArrowLeft', 'ArrowRight', 'Home', 'End'] as const

export type LedgerTabKey = typeof LEDGER_TAB_KEYS[number]

export function nextLedgerTab(
  tabs: WalletType[],
  current: WalletType,
  key: string,
): WalletType | null {
  if (!LEDGER_TAB_KEYS.some((supportedKey) => supportedKey === key)) return null
  if (tabs.length === 0) return null

  const currentIndex = Math.max(0, tabs.indexOf(current))
  if (key === 'Home') return tabs[0] ?? null
  if (key === 'End') return tabs.at(-1) ?? null
  if (key === 'ArrowLeft') return tabs[(currentIndex - 1 + tabs.length) % tabs.length] ?? null
  return tabs[(currentIndex + 1) % tabs.length] ?? null
}
