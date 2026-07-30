package wallet

import "errors"

// Minimum withdrawal amount (plan §5.2). A wallet may override it via the
// admin-only DynamoDB field Wallet.MinWithdrawal; an unset (zero) override
// falls back to the default here. Mirrors the deposit-range override scheme
// (deposit_limits.go).
const (
	DefaultMinWithdrawal = 100 // R$ 1,00 in centavos

	// AbsoluteMinWithdrawal is a hard floor: no per-wallet override may allow a
	// withdrawal below this, mirroring AbsoluteMinDeposit's reasoning — below it
	// the PIX transfer cost can exceed the amount moved.
	AbsoluteMinWithdrawal = 100 // R$ 1,00 in centavos
)

// ErrWithdrawalBelowMinimum reports an amount below the wallet's effective
// minimum withdrawal, outside the fullBalance/isClosure carve-outs.
var ErrWithdrawalBelowMinimum = errors.New("withdrawal amount below minimum")

// MinWithdrawal returns the effective minimum withdrawal amount in centavos
// for w. Per-wallet MinWithdrawal overrides the default when set (>0); never
// below AbsoluteMinWithdrawal. Pass w == nil for the default.
func MinWithdrawal(w *Wallet) int64 {
	minAmt := int64(DefaultMinWithdrawal)
	if w != nil && w.MinWithdrawal > 0 {
		minAmt = w.MinWithdrawal
	}
	if minAmt < AbsoluteMinWithdrawal {
		minAmt = AbsoluteMinWithdrawal
	}
	return minAmt
}

// ValidateWithdrawalAmount enforces the effective minimum — EXCEPT when the
// withdrawal empties the wallet's full balance (fullBalance=true) or is a
// closure payout (isClosure=true), both required carve-outs (plan §5.2): a
// full-balance withdrawal below the minimum must never trap a user under the
// floor from reaching their own money, and a closure payout must be able to
// terminate the account regardless of size.
func ValidateWithdrawalAmount(amount int64, w *Wallet, fullBalance, isClosure bool) error {
	if fullBalance || isClosure {
		return nil
	}
	if amount < MinWithdrawal(w) {
		return ErrWithdrawalBelowMinimum
	}
	return nil
}
