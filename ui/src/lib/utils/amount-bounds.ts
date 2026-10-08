interface AmountBoundsInput {
  /** Static ceiling of the flow (the R$ 1.000.000 ring-fence cap), or Infinity. */
  ceiling: number
  /** Available balance for flows capped by it, or Infinity. */
  balance: number
  /** Server-computed "allowed right now" (daily limits already folded in); undefined = unknown. */
  limitNow?: number
  /** Server-reported minimum; undefined = 1 centavo. */
  min?: number
}

export interface AmountBounds {
  min: number
  max: number
  /** True when the server says nothing can be moved right now (daily limit used up). */
  blocked: boolean
}

/**
 * The amount range a dialog may accept. Pure so it is testable; the server stays
 * the authority (it re-checks every limit), this only keeps the form honest.
 */
export function amountBounds({ceiling, balance, limitNow, min}: AmountBoundsInput): AmountBounds {
  const blocked = limitNow === 0
  const max = Math.min(ceiling, balance, limitNow ?? Number.POSITIVE_INFINITY)
  // A minimum above the maximum can only be the "empty the whole balance" case.
  return {min: Math.min(min ?? 1, max), max, blocked}
}
