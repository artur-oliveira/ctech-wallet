package wallet

// Withdrawal fee defaults (design spec §D). A wallet may override any of these
// via admin-only DynamoDB fields (Wallet.FeeBps/FeeMin/FeeMax); an unset (zero)
// override falls back to the default here.
const (
	DefaultFeeBps = 200  // 2.00% in basis points
	DefaultFeeMin = 100  // R$ 1,00 in centavos
	DefaultFeeMax = 1000 // R$ 10,00 in centavos

	// AbsoluteFeeMin is a hard floor: the effective fee can never be below this,
	// even if a wallet configures a lower FeeMin. Covers the PIX transfer cost.
	AbsoluteFeeMin = 100 // R$ 1,00 in centavos
)

// WithdrawalFee returns the fee in centavos for withdrawing amount from w.
// Per-wallet FeeBps/FeeMin/FeeMax override the defaults when set (>0); the
// result is clamped to [effectiveMin, effectiveMax] and never below
// AbsoluteFeeMin. Pass w == nil to use the defaults. Integer math only.
func WithdrawalFee(amount int64, w *Wallet) int64 {
	bps, minFee, maxFee := int64(DefaultFeeBps), int64(DefaultFeeMin), int64(DefaultFeeMax)
	if w != nil {
		if w.FeeBps > 0 {
			bps = w.FeeBps
		}
		if w.FeeMin > 0 {
			minFee = w.FeeMin
		}
		if w.FeeMax > 0 {
			maxFee = w.FeeMax
		}
	}
	// Hard floor — no per-wallet override may push the fee below the absolute min.
	if minFee < AbsoluteFeeMin {
		minFee = AbsoluteFeeMin
	}
	if maxFee < minFee {
		maxFee = minFee
	}

	fee := amount * bps / 10000
	if fee < minFee {
		return minFee
	}
	if fee > maxFee {
		return maxFee
	}
	return fee
}
