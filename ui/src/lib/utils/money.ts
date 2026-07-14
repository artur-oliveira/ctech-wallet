/**
 * Money helpers. The API speaks integer centavos everywhere — never floats.
 * Real balance is rendered as BRL; sandbox is deliberately rendered WITHOUT a
 * currency symbol, because sandbox credit has no monetary value and is never
 * convertible to real money.
 *
 * Formatting is locale-aware: callers may pass a locale, otherwise it follows
 * the active i18n language so English users see "R$1,234.56" and pt-BR users
 * see "R$ 1.234,56" — the R$ symbol is kept (BRL is always the currency).
 */

import i18n from '@/lib/i18n'

const brlCache = new Map<string, Intl.NumberFormat>()
const plainCache = new Map<string, Intl.NumberFormat>()

function brl(locale: string): Intl.NumberFormat {
  let f = brlCache.get(locale)
  if (!f) {
    f = new Intl.NumberFormat(locale, {style: 'currency', currency: 'BRL'})
    brlCache.set(locale, f)
  }
  return f
}

function plain(locale: string): Intl.NumberFormat {
  let f = plainCache.get(locale)
  if (!f) {
    f = new Intl.NumberFormat(locale, {minimumFractionDigits: 2, maximumFractionDigits: 2})
    plainCache.set(locale, f)
  }
  return f
}

/** Hard ceiling on any user-typed amount: R$ 1.000.000,00 = 100.000.000 centavos. */
export const MAX_AMOUNT_CENTS = 100_000_000

/** Max digits a user can type before hitting MAX_AMOUNT_CENTS (9 = 100.000.000). */
export const MAX_AMOUNT_DIGITS = 9

/** Formats integer centavos as BRL, e.g. 12345 → "R$ 123,45". */
export function formatBRL(centavos: number, locale: string = i18n.language || 'pt-BR'): string {
  return brl(locale).format(centavos / 100)
}

/** Formats integer centavos as a bare number — used for sandbox credit. */
export function formatCredits(centavos: number, locale: string = i18n.language || 'pt-BR'): string {
  return plain(locale).format(centavos / 100)
}

/** Signed centavos → "+R$ 10,00" / "−R$ 10,00" for ledger rows. */
export function formatSigned(centavos: number, real: boolean, locale: string = i18n.language || 'pt-BR'): string {
  const sign = centavos < 0 ? '−' : '+'
  const abs = Math.abs(centavos)
  return `${sign}${real ? formatBRL(abs, locale) : formatCredits(abs, locale)}`
}
