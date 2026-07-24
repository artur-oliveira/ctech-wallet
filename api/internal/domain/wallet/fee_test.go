package wallet

import "testing"

func TestWithdrawalFeeDefaults(t *testing.T) {
	cases := []struct {
		name         string
		amount, want int64
	}{
		{"tiny clamps to min", 100, 100},
		{"2pct equals min exactly", 5000, 100},
		{"just above min threshold floors to min", 5001, 100},
		{"mid range", 10000, 200},
		{"2pct equals max exactly", 50000, 1000},
		{"above max clamps", 60000, 1000},
		{"large clamps to max", 1000000, 1000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := WithdrawalFee(tc.amount, nil); got != tc.want {
				t.Errorf("WithdrawalFee(%d, nil) = %d, want %d", tc.amount, got, tc.want)
			}
		})
	}
}

func TestWithdrawalFeePerWalletOverride(t *testing.T) {
	// A wallet with a 1% rate, higher min, higher cap.
	w := &Wallet{FeeBps: 100, FeeMin: 200, FeeMax: 5000}
	// 1% of 100000 = 1000, within [200, 5000].
	if got := WithdrawalFee(100000, w); got != 1000 {
		t.Errorf("override mid: got %d, want 1000", got)
	}
	// 1% of 10000 = 100 → below wallet min 200 → 200.
	if got := WithdrawalFee(10000, w); got != 200 {
		t.Errorf("override min: got %d, want 200", got)
	}
	// 1% of 1000000 = 10000 → above wallet max 5000 → 5000.
	if got := WithdrawalFee(1000000, w); got != 5000 {
		t.Errorf("override max: got %d, want 5000", got)
	}
}

func TestWithdrawalFeeAbsoluteFloor(t *testing.T) {
	// Admin tries to set a fee_min below the absolute floor — it must not apply.
	w := &Wallet{FeeBps: 100, FeeMin: 10, FeeMax: 5000}
	// 1% of 100 = 1, would clamp to wallet min 10, but AbsoluteFeeMin (100) wins.
	if got := WithdrawalFee(100, w); got != AbsoluteFeeMin {
		t.Errorf("absolute floor: got %d, want %d", got, AbsoluteFeeMin)
	}
	// Even a large-ish amount whose 1% is under the floor stays at the floor.
	if got := WithdrawalFee(5000, w); got != AbsoluteFeeMin {
		t.Errorf("absolute floor mid: got %d, want %d", got, AbsoluteFeeMin)
	}
}
