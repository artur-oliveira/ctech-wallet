package wallet

import "testing"

func TestDepositLimitsDefaults(t *testing.T) {
	minAmt, maxAmt := DepositLimits(nil)
	if minAmt != DefaultMinDeposit {
		t.Errorf("min = %d, want %d", minAmt, DefaultMinDeposit)
	}
	if maxAmt != DefaultMaxDeposit {
		t.Errorf("max = %d, want %d", maxAmt, DefaultMaxDeposit)
	}
}

func TestDepositLimitsPerWalletOverride(t *testing.T) {
	w := &Wallet{MinDeposit: 5000, MaxDeposit: 20000}
	minAmt, maxAmt := DepositLimits(w)
	if minAmt != 5000 || maxAmt != 20000 {
		t.Errorf("override: got [%d, %d], want [5000, 20000]", minAmt, maxAmt)
	}

	// A single unset field falls back to its default independently.
	onlyMax := &Wallet{MaxDeposit: 20000}
	minAmt, maxAmt = DepositLimits(onlyMax)
	if minAmt != DefaultMinDeposit || maxAmt != 20000 {
		t.Errorf("partial override: got [%d, %d], want [%d, 20000]", minAmt, maxAmt, DefaultMinDeposit)
	}
}

func TestDepositLimitsAbsoluteFloor(t *testing.T) {
	// Admin tries to set a min_deposit below the global R$ 1,00 floor.
	w := &Wallet{MinDeposit: 1, MaxDeposit: 20000}
	minAmt, _ := DepositLimits(w)
	if minAmt != AbsoluteMinDeposit {
		t.Errorf("absolute floor: got %d, want %d", minAmt, AbsoluteMinDeposit)
	}
}

func TestDepositLimitsIncoherentOverrideDoesNotInvert(t *testing.T) {
	// max below min must not produce an empty range that rejects every amount.
	w := &Wallet{MinDeposit: 10000, MaxDeposit: 5000}
	minAmt, maxAmt := DepositLimits(w)
	if maxAmt < minAmt {
		t.Errorf("inverted range [%d, %d]", minAmt, maxAmt)
	}
}

func TestValidateDepositAmount(t *testing.T) {
	cases := []struct {
		name   string
		amount int64
		w      *Wallet
		ok     bool
	}{
		{"below global min", 99, nil, false},
		{"exactly global min", 100, nil, true},
		{"mid range", 50000, nil, true},
		{"exactly global max", DefaultMaxDeposit, nil, true},
		{"above global max", DefaultMaxDeposit + 1, nil, false},
		{"zero", 0, nil, false},
		{"negative", -100, nil, false},
		{"below wallet min", 4999, &Wallet{MinDeposit: 5000, MaxDeposit: 20000}, false},
		{"exactly wallet min", 5000, &Wallet{MinDeposit: 5000, MaxDeposit: 20000}, true},
		{"exactly wallet max", 20000, &Wallet{MinDeposit: 5000, MaxDeposit: 20000}, true},
		{"above wallet max", 20001, &Wallet{MinDeposit: 5000, MaxDeposit: 20000}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateDepositAmount(tc.amount, tc.w)
			if tc.ok && err != nil {
				t.Errorf("ValidateDepositAmount(%d) = %v, want nil", tc.amount, err)
			}
			if !tc.ok && err == nil {
				t.Errorf("ValidateDepositAmount(%d) = nil, want error", tc.amount)
			}
		})
	}
}
