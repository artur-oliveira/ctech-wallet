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
			if got := WithdrawalFee(tc.amount, nil, false); got != tc.want {
				t.Errorf("WithdrawalFee(%d, nil, false) = %d, want %d", tc.amount, got, tc.want)
			}
		})
	}
}

func TestWithdrawalFeePerWalletOverride(t *testing.T) {
	// A wallet with a 1% rate, higher min, higher cap.
	w := &Wallet{FeeBps: 100, FeeMin: 200, FeeMax: 5000}
	// 1% of 100000 = 1000, within [200, 5000].
	if got := WithdrawalFee(100000, w, false); got != 1000 {
		t.Errorf("override mid: got %d, want 1000", got)
	}
	// 1% of 10000 = 100 → below wallet min 200 → 200.
	if got := WithdrawalFee(10000, w, false); got != 200 {
		t.Errorf("override min: got %d, want 200", got)
	}
	// 1% of 1000000 = 10000 → above wallet max 5000 → 5000.
	if got := WithdrawalFee(1000000, w, false); got != 5000 {
		t.Errorf("override max: got %d, want 5000", got)
	}
}

func TestWithdrawalFeeAbsoluteFloor(t *testing.T) {
	// Admin tries to set a fee_min below the absolute floor — it must not apply.
	w := &Wallet{FeeBps: 100, FeeMin: 10, FeeMax: 5000}
	// 1% of 100 = 1, would clamp to wallet min 10, but AbsoluteFeeMin (100) wins.
	if got := WithdrawalFee(100, w, false); got != AbsoluteFeeMin {
		t.Errorf("absolute floor: got %d, want %d", got, AbsoluteFeeMin)
	}
	// Even a large-ish amount whose 1% is under the floor stays at the floor.
	if got := WithdrawalFee(5000, w, false); got != AbsoluteFeeMin {
		t.Errorf("absolute floor mid: got %d, want %d", got, AbsoluteFeeMin)
	}
}

func TestWithdrawalFeeClosureIsAlwaysFree(t *testing.T) {
	// A closure payout is fee-free regardless of amount or per-wallet overrides
	// — precondition for POST /wallet/closure to ever terminate (plan §5.2).
	w := &Wallet{FeeBps: 500, FeeMin: 1000, FeeMax: 50000}
	for _, amount := range []int64{1, 100, 100000, 10000000} {
		if got := WithdrawalFee(amount, w, true); got != 0 {
			t.Errorf("closure fee for amount %d: got %d, want 0", amount, got)
		}
	}
	if got := WithdrawalFee(100000, nil, true); got != 0 {
		t.Errorf("closure fee with nil wallet: got %d, want 0", got)
	}
}
