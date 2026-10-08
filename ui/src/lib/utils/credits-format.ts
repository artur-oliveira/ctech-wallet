/**
 * Sandbox credits are the virtual currency: whole numbers only (the API stores
 * them as integers), shown with their own symbol. Dependency-free so it can be
 * unit-tested with node --test; money.ts re-exports the user-facing helpers.
 */

/** Symbol of the virtual currency (colon sign, from "CTech"). Never "R$": credits have no monetary value. */
export const CREDIT_SYMBOL = '₡' // ₡

const NBSP = ' '

const formatters = new Map<string, Intl.NumberFormat>()

function integerFormat(locale: string): Intl.NumberFormat {
  let f = formatters.get(locale)
  if (!f) {
    f = new Intl.NumberFormat(locale, {maximumFractionDigits: 0})
    formatters.set(locale, f)
  }
  return f
}

/**
 * Formats raw sandbox credits (NOT centavos): "₡ 1.000" (pt) / "₡1,000" (en).
 * `signed` prefixes "+" or "−" (true minus) before the symbol, for ledger rows.
 */
export function formatCreditValue(credits: number, locale: string, signed = false): string {
  const gap = locale.startsWith('pt') ? NBSP : ''
  const body = `${CREDIT_SYMBOL}${gap}${integerFormat(locale).format(Math.abs(credits))}`
  if (!signed) return credits < 0 ? `−${body}` : body
  return `${credits < 0 ? '−' : '+'}${body}`
}
