package wallet

import "errors"

// PIX deposit amount limits. A wallet may override either bound via admin-only
// DynamoDB fields (Wallet.MinDeposit/MaxDeposit); an unset (zero) override falls
// back to the default here. Mirrors the withdrawal-fee override scheme.
const (
	DefaultMinDeposit = 100     // R$ 1,00 in centavos
	DefaultMaxDeposit = 1000000 // R$ 10.000,00 in centavos

	// AbsoluteMinDeposit is a hard floor: no per-wallet override may allow a
	// deposit below this, so a misconfigured wallet can never accept dust
	// deposits whose PIX cost exceeds the amount credited.
	AbsoluteMinDeposit = 100 // R$ 1,00 in centavos
)

// ErrDepositOutOfRange reports an amount outside the wallet's accepted deposit
// range. The service maps it to problem.DepositOutOfRange.
var ErrDepositOutOfRange = errors.New("deposit amount out of range")

// DepositLimits returns the effective [min, max] deposit range in centavos for
// w. Per-wallet MinDeposit/MaxDeposit override the defaults when set (>0); min
// is never below AbsoluteMinDeposit, and an incoherent override (max < min) is
// widened to min so the range can never be empty. Pass w == nil for defaults.
func DepositLimits(w *Wallet) (minAmt, maxAmt int64) {
	minAmt, maxAmt = int64(DefaultMinDeposit), int64(DefaultMaxDeposit)
	if w != nil {
		if w.MinDeposit > 0 {
			minAmt = w.MinDeposit
		}
		if w.MaxDeposit > 0 {
			maxAmt = w.MaxDeposit
		}
	}
	if minAmt < AbsoluteMinDeposit {
		minAmt = AbsoluteMinDeposit
	}
	if maxAmt < minAmt {
		maxAmt = minAmt
	}
	return minAmt, maxAmt
}

// ValidateDepositAmount reports whether amount is within w's deposit range.
func ValidateDepositAmount(amount int64, w *Wallet) error {
	minAmt, maxAmt := DepositLimits(w)
	if amount < minAmt || amount > maxAmt {
		return ErrDepositOutOfRange
	}
	return nil
}
