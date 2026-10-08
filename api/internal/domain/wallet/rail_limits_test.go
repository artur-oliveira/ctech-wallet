package wallet

import "testing"

func TestComputeRailLimitsDefaultsFresh(t *testing.T) {
	l := ComputeRailLimits(&Wallet{Balance: 500000}, RealDailyCounters{}, dailyNow)
	if l.Deposit.Min != 100 || l.Deposit.Max != 1000000 {
		t.Fatalf("deposit range = %d..%d", l.Deposit.Min, l.Deposit.Max)
	}
	// R$1.000 daily cap is tighter than the R$10.000 range max.
	if l.Deposit.DailyCap != 100000 || l.Deposit.DailyRemaining != 100000 || l.Deposit.MaxNow != 100000 {
		t.Fatalf("deposit = %+v", l.Deposit)
	}
	if l.Withdraw.MaxNow != 100000 || l.Withdraw.CountRemaining != 1 {
		t.Fatalf("withdraw = %+v", l.Withdraw)
	}
	if l.ResetsAt == "" {
		t.Fatal("resets_at must be set")
	}
}

func TestComputeRailLimitsUsedToday(t *testing.T) {
	day, _, _ := WindowKeys(dailyNow)
	c := RealDailyCounters{DayKey: day, DepositSum: 60000, WithdrawSum: 100000, WithdrawCount: 1}
	l := ComputeRailLimits(&Wallet{Balance: 500000}, c, dailyNow)
	if l.Deposit.DailyRemaining != 40000 || l.Deposit.MaxNow != 40000 {
		t.Fatalf("deposit = %+v", l.Deposit)
	}
	if l.Withdraw.CountRemaining != 0 || l.Withdraw.MaxNow != 0 {
		t.Fatalf("a used-up daily withdrawal must report MaxNow 0: %+v", l.Withdraw)
	}
}

func TestComputeRailLimitsYesterdayDoesNotCount(t *testing.T) {
	c := RealDailyCounters{DayKey: "2026-10-07", DepositSum: 100000, WithdrawSum: 100000, WithdrawCount: 1}
	l := ComputeRailLimits(&Wallet{Balance: 500000}, c, dailyNow)
	if l.Deposit.MaxNow != 100000 || l.Withdraw.MaxNow != 100000 {
		t.Fatalf("limits = %+v", l)
	}
}

func TestComputeRailLimitsWithdrawBoundedByBalanceAndMinimum(t *testing.T) {
	if got := ComputeRailLimits(&Wallet{Balance: 30000}, RealDailyCounters{}, dailyNow).Withdraw.MaxNow; got != 30000 {
		t.Fatalf("balance below the cap: MaxNow = %d, want the balance", got)
	}
	// Below the 100-centavo minimum but it empties the wallet: allowed.
	if got := ComputeRailLimits(&Wallet{Balance: 50}, RealDailyCounters{}, dailyNow).Withdraw.MaxNow; got != 50 {
		t.Fatalf("full-balance exemption: MaxNow = %d", got)
	}
	if got := ComputeRailLimits(&Wallet{Balance: 0}, RealDailyCounters{}, dailyNow).Withdraw.MaxNow; got != 0 {
		t.Fatalf("empty wallet: MaxNow = %d", got)
	}
	// Remaining daily room (50) below the minimum and below the balance: cannot withdraw.
	day, _, _ := WindowKeys(dailyNow)
	c := RealDailyCounters{DayKey: day, WithdrawSum: 99950}
	lim := Wallet{Balance: 500000, DailyWithdrawCount: 2}
	if got := ComputeRailLimits(&lim, c, dailyNow).Withdraw.MaxNow; got != 0 {
		t.Fatalf("room below the minimum: MaxNow = %d, want 0", got)
	}
}

func TestComputeRailLimitsHonoursOverrides(t *testing.T) {
	w := &Wallet{Balance: 900000, MinDeposit: 500, MaxDeposit: 20000, DailyDepositCap: 50000}
	l := ComputeRailLimits(w, RealDailyCounters{}, dailyNow)
	if l.Deposit.Min != 500 || l.Deposit.Max != 20000 || l.Deposit.MaxNow != 20000 {
		t.Fatalf("deposit = %+v", l.Deposit)
	}
}
