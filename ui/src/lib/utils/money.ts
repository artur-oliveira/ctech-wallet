/**
 * Money helpers. The API speaks integer centavos everywhere — never floats.
 * Real balance is rendered as BRL; sandbox is deliberately rendered WITHOUT a
 * currency symbol, because sandbox credit has no monetary value and is never
 * convertible to real money.
 */

const BRL = new Intl.NumberFormat('pt-BR', {style: 'currency', currency: 'BRL'})
const PLAIN = new Intl.NumberFormat('pt-BR', {minimumFractionDigits: 2, maximumFractionDigits: 2})

/** Hard ceiling on any user-typed amount: R$ 1.000.000,00 = 100.000.000 centavos. */
export const MAX_AMOUNT_CENTS = 100_000_000

/** Max digits a user can type before hitting MAX_AMOUNT_CENTS (9 = 100.000.000). */
export const MAX_AMOUNT_DIGITS = 9

/** Formats integer centavos as BRL, e.g. 12345 → "R$ 123,45". */
export function formatBRL(centavos: number): string {
  return BRL.format(centavos / 100)
}

/** Formats integer centavos as a bare number — used for sandbox credit. */
export function formatCredits(centavos: number): string {
  return PLAIN.format(centavos / 100)
}

/** Signed centavos → "+R$ 10,00" / "−R$ 10,00" for ledger rows. */
export function formatSigned(centavos: number, real: boolean): string {
  const sign = centavos < 0 ? '−' : '+'
  const abs = Math.abs(centavos)
  return `${sign}${real ? formatBRL(abs) : formatCredits(abs)}`
}
