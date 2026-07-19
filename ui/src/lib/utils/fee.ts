/** Withdrawal fee rules mirrored from api/internal/domain/wallet/fee.go. */
export const FEE_BPS = 200 // 2.00%
export const FEE_MIN = 100 // R$ 1,00
export const FEE_MAX = 1000 // R$ 10,00
export const FEE_ABSOLUTE_MIN = 100 // R$ 1,00 — never below
const BASIS_POINTS_DIVISOR = 10_000

export interface WithdrawalFeeConfig {
    fee_bps?: number
    fee_min?: number
    fee_max?: number
}

function positiveOverride(value: number | undefined, fallback: number): number {
    return value != null && value > 0 ? value : fallback
}

/**
 * Fee in integer centavos. Positive wallet values override defaults; an
 * incoherent maximum widens to the effective minimum, exactly as the API does.
 */
export function withdrawalFee(amount: number, config?: WithdrawalFeeConfig): number {
    const bps = positiveOverride(config?.fee_bps, FEE_BPS)
    const min = Math.max(FEE_ABSOLUTE_MIN, positiveOverride(config?.fee_min, FEE_MIN))
    const configuredMax = positiveOverride(config?.fee_max, FEE_MAX)
    const max = Math.max(min, configuredMax)
    const feeRaw = Math.floor(amount * bps / BASIS_POINTS_DIVISOR)

    if (feeRaw < min) return min
    if (feeRaw > max) return max
    return feeRaw
}

/** Largest withdrawal whose amount plus configured fee fits the balance. */
export function maxWithdrawable(balance: number, config?: WithdrawalFeeConfig): number {
    let low = 0
    let high = Math.max(0, Math.floor(balance))

    while (low < high) {
        const candidate = Math.ceil((low + high) / 2)
        if (candidate + withdrawalFee(candidate, config) <= balance) {
            low = candidate
        } else {
            high = candidate - 1
        }
    }

    return low
}
