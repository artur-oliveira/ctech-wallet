export const LEDGER_TAB_KEYS = ['ArrowLeft', 'ArrowRight', 'Home', 'End'] as const

export type LedgerTabKey = typeof LEDGER_TAB_KEYS[number]

export function nextLedgerTab<T extends string>(
  tabs: T[],
  current: T,
  key: string,
): T | null {
  if (!LEDGER_TAB_KEYS.some((supportedKey) => supportedKey === key)) return null
  if (tabs.length === 0) return null

  const currentIndex = Math.max(0, tabs.indexOf(current))
  if (key === 'Home') return tabs[0] ?? null
  if (key === 'End') return tabs.at(-1) ?? null
  if (key === 'ArrowLeft') return tabs[(currentIndex - 1 + tabs.length) % tabs.length] ?? null
  return tabs[(currentIndex + 1) % tabs.length] ?? null
}

export const PURCHASES_TAB = 'purchases'

/**
 * The statements a user can open: the money balance (`real`), the Sandbox history when a Sandbox
 * wallet exists, and the old purchases. The game wallet was merged into `real` and the gambling
 * rules moved to Poker, so there is no `game` statement and no activation any more.
 */
export function availableLedgerTabs(hasSandbox: boolean): Array<'real' | 'sandbox' | typeof PURCHASES_TAB> {
  const wallets: Array<'real' | 'sandbox'> = ['real']
  if (hasSandbox) wallets.push('sandbox')
  return [...wallets, PURCHASES_TAB]
}
