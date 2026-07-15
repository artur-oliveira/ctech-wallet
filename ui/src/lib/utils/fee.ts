/**
 * Withdrawal fee, mirrored from api/internal/domain/wallet/fee.go so the UI
 * preview matches what the backend actually debits. Per-wallet FeeBps/FeeMin/
 * FeeMax are admin-only and unknown to the client, so we show the documented
 * defaults; the backend remains the source of truth on commit.
 *
 * Formula (integer math, centavos):
 *   fee = clamp(amount * 200 / 10000, max(AbsoluteFeeMin, min), max)
 *   AbsoluteFeeMin = 100 (R$ 1,00) is a hard floor nothing may go below.
 *
 * Transfers between real and game carry NO fee in either direction (CLAUDE.md).
 */
export const FEE_BPS = 200 // 2.00%
export const FEE_MIN = 100 // R$ 1,00
export const FEE_MAX = 1000 // R$ 10,00
export const FEE_ABSOLUTE_MIN = 100 // R$ 1,00 — never below

/** Withdrawal fee in centavos for `amount` centavos, using the documented defaults. */
export function withdrawalFee(amount: number): number {
    const feeRaw = (amount * FEE_BPS) / 10000
    const min = Math.max(FEE_ABSOLUTE_MIN, FEE_MIN)
    if (feeRaw < min) return min
    if (feeRaw > FEE_MAX) return FEE_MAX
    return Math.floor(feeRaw)
}
