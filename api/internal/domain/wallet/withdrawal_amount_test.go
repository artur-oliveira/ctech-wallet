package wallet

import "testing"

func TestMinWithdrawalDefaults(t *testing.T) {
	if got := MinWithdrawal(nil); got != DefaultMinWithdrawal {
		t.Errorf("got %d, want %d", got, DefaultMinWithdrawal)
	}
}

func TestMinWithdrawalPerWalletOverride(t *testing.T) {
	w := &Wallet{MinWithdrawal: 500}
	if got := MinWithdrawal(w); got != 500 {
		t.Errorf("got %d, want 500", got)
	}
}

func TestMinWithdrawalNeverBelowAbsoluteFloor(t *testing.T) {
	// Admin tries to set a floor below the absolute minimum — it must not apply.
	w := &Wallet{MinWithdrawal: 1}
	if got := MinWithdrawal(w); got != AbsoluteMinWithdrawal {
		t.Errorf("got %d, want %d", got, AbsoluteMinWithdrawal)
	}
}

func TestValidateWithdrawalAmountBoundary(t *testing.T) {
	if err := ValidateWithdrawalAmount(AbsoluteMinWithdrawal, nil, false, false); err != nil {
		t.Errorf("amount at minimum must be accepted: %v", err)
	}
	if err := ValidateWithdrawalAmount(AbsoluteMinWithdrawal-1, nil, false, false); err != ErrWithdrawalBelowMinimum {
		t.Errorf("amount below minimum: got %v, want ErrWithdrawalBelowMinimum", err)
	}
}

func TestValidateWithdrawalAmountFullBalanceCarveOut(t *testing.T) {
	// A full-balance withdrawal below the minimum must never trap a user under
	// the floor from reaching their own money (plan §5.2).
	if err := ValidateWithdrawalAmount(1, nil, true, false); err != nil {
		t.Errorf("full-balance withdrawal must bypass the minimum: %v", err)
	}
}

func TestValidateWithdrawalAmountClosureCarveOut(t *testing.T) {
	// A closure payout must be able to terminate the account regardless of size
	// — precondition for POST /wallet/closure to ever complete (plan §5.2).
	if err := ValidateWithdrawalAmount(1, nil, false, true); err != nil {
		t.Errorf("closure payout must bypass the minimum: %v", err)
	}
}
