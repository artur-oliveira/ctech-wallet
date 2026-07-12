/**
 * Money helpers. The API speaks integer centavos everywhere — never floats.
 * Real balance is rendered as BRL; sandbox is deliberately rendered WITHOUT a
 * currency symbol, because sandbox credit has no monetary value and is never
 * convertible to real money.
 */

const BRL = new Intl.NumberFormat('pt-BR', {style: 'currency', currency: 'BRL'})
const PLAIN = new Intl.NumberFormat('pt-BR', {minimumFractionDigits: 2, maximumFractionDigits: 2})

/** Formats integer centavos as BRL, e.g. 12345 → "R$ 123,45". */
export function formatBRL(centavos: number): string {
  return BRL.format(centavos / 100)
}

/** Formats integer centavos as a bare number — used for sandbox credit. */
export function formatCredits(centavos: number): string {
  return PLAIN.format(centavos / 100)
}

/** Parses a user-typed amount ("123,45" or "123.45") into integer centavos. */
export function parseCentavos(input: string): number | null {
  const normalized = input.replace(/\s/g, '').replace(/\./g, '').replace(',', '.')
  if (!/^\d+(\.\d{1,2})?$/.test(normalized)) return null
  return Math.round(parseFloat(normalized) * 100)
}

/** Signed centavos → "+R$ 10,00" / "−R$ 10,00" for ledger rows. */
export function formatSigned(centavos: number, real: boolean): string {
  const sign = centavos < 0 ? '−' : '+'
  const abs = Math.abs(centavos)
  return `${sign}${real ? formatBRL(abs) : formatCredits(abs)}`
}
