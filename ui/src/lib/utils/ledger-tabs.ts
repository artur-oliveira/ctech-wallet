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
 * The statements a user can open. `game` appears only while the user still has a
 * game wallet: after the merge there is one money balance (`real`) and no game
 * statement to show.
 */
export function availableLedgerTabs(
  activated: boolean,
  hasSandbox: boolean,
  hasGame: boolean,
): Array<'real' | 'game' | 'sandbox' | typeof PURCHASES_TAB> {
  const wallets: Array<'real' | 'game' | 'sandbox'> = ['real']
  if (activated && hasGame) wallets.push('game')
  if (activated || hasSandbox) wallets.push('sandbox')
  return [...wallets, PURCHASES_TAB]
}
