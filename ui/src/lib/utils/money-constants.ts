/**
 * Money constants, dependency-free so the node --test sync check can import
 * them. Values are pinned to rpc-contract/money.json (the canonical
 * cross-language source, B18) by money-contract.test.mjs.
 */

/** Hard ceiling on any user-typed amount: R$ 1.000.000,00 = 100.000.000 centavos. */
export const MAX_AMOUNT_CENTS = 100_000_000

/** Max digits a user can type before hitting MAX_AMOUNT_CENTS (9 = 100.000.000). */
export const MAX_AMOUNT_DIGITS = 9

/**
 * Fixed sandbox conversion rate: R$ 1,00 (100 centavos) = 1000 credits.
 * The rate is a backend constant, never client-supplied.
 */
export const SANDBOX_CREDITS_PER_CENTAVO = 10
