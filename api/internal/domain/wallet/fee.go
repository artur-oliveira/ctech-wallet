package wallet

import rpccontract "gopkg.aoctech.app/wallet/rpc-contract"

// Withdrawal fee defaults (design spec §D). A wallet may override any of these
// via admin-only DynamoDB fields (Wallet.FeeBps/FeeMin/FeeMax); an unset (zero)
// override falls back to the default here. Values are defined once in
// rpc-contract (money.json, shared with the ui — B18).
const (
	DefaultFeeBps = rpccontract.DefaultFeeBps
	DefaultFeeMin = rpccontract.DefaultFeeMin
	DefaultFeeMax = rpccontract.DefaultFeeMax

	// AbsoluteFeeMin is a hard floor: the effective fee can never be below this,
	// even if a wallet configures a lower FeeMin. Covers the PIX transfer cost.
	AbsoluteFeeMin = rpccontract.AbsoluteFeeMin
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
