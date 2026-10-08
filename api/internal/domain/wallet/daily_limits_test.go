package wallet

import (
	"testing"
	"time"
)

var dailyNow = time.Date(2026, 10, 8, 15, 0, 0, 0, saoPaulo)

func TestEffectiveDailyLimitsDefaults(t *testing.T) {
	got := EffectiveDailyLimits(&Wallet{})
	want := DailyLimits{DepositCap: 100000, WithdrawCap: 100000, WithdrawCount: 1}
	if got != want {
		t.Fatalf("got %+v want %+v", got, want)
	}
	if EffectiveDailyLimits(nil) != want {
		t.Fatal("nil wallet must yield defaults")
	}
}

func TestEffectiveDailyLimitsOverride(t *testing.T) {
	got := EffectiveDailyLimits(&Wallet{DailyDepositCap: 50000, DailyWithdrawCap: 20000, DailyWithdrawCount: 3})
	want := DailyLimits{DepositCap: 50000, WithdrawCap: 20000, WithdrawCount: 3}
	if got != want {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestCheckDailyDeposit(t *testing.T) {
	lim := DailyLimits{DepositCap: 100000}
	day, _, _ := WindowKeys(dailyNow)
	cases := []struct {
		name   string
		c      RealDailyCounters
		amount int64
		breach bool
	}{
		{"fresh exactly cap", RealDailyCounters{}, 100000, false},
		{"fresh over cap", RealDailyCounters{}, 100001, true},
		{"accumulated within", RealDailyCounters{DayKey: day, DepositSum: 60000}, 40000, false},
		{"accumulated over", RealDailyCounters{DayKey: day, DepositSum: 60000}, 40001, true},
		{"yesterday does not count", RealDailyCounters{DayKey: "2026-10-07", DepositSum: 100000}, 100000, false},
		{"counter drifted above cap", RealDailyCounters{DayKey: day, DepositSum: 200000}, 1, true},
	}
	for _, tc := range cases {
		got := CheckDailyDeposit(lim, tc.c, tc.amount, dailyNow)
		if (got != nil) != tc.breach {
			t.Errorf("%s: breach=%v want %v", tc.name, got != nil, tc.breach)
		}
		if got != nil && got.Kind != DailyBreachDepositCap {
			t.Errorf("%s: kind %q", tc.name, got.Kind)
		}
	}
}

func TestCheckDailyWithdraw(t *testing.T) {
	lim := DailyLimits{WithdrawCap: 100000, WithdrawCount: 1}
	day, _, _ := WindowKeys(dailyNow)

	if b := CheckDailyWithdraw(lim, RealDailyCounters{}, 100000, dailyNow); b != nil {
		t.Fatalf("first full-cap withdrawal must pass, got %+v", b)
	}
	if b := CheckDailyWithdraw(lim, RealDailyCounters{}, 100001, dailyNow); b == nil || b.Kind != DailyBreachWithdrawCap {
		t.Fatalf("over cap: %+v", b)
	}
	if b := CheckDailyWithdraw(lim, RealDailyCounters{DayKey: day, WithdrawCount: 1, WithdrawSum: 100}, 100, dailyNow); b == nil || b.Kind != DailyBreachWithdrawCount {
		t.Fatalf("second withdrawal must breach count: %+v", b)
	}
	if b := CheckDailyWithdraw(lim, RealDailyCounters{DayKey: "2026-10-07", WithdrawCount: 1}, 100, dailyNow); b != nil {
		t.Fatalf("yesterday must not count: %+v", b)
	}
}

func TestDayRolloverBoundary(t *testing.T) {
	lim := DailyLimits{DepositCap: 100000}
	late := time.Date(2026, 10, 8, 23, 59, 59, 0, saoPaulo)
	early := time.Date(2026, 10, 9, 0, 0, 1, 0, saoPaulo)
	lateDay, _, _ := WindowKeys(late)
	c := RealDailyCounters{DayKey: lateDay, DepositSum: 100000}
	if CheckDailyDeposit(lim, c, 1, late) == nil {
		t.Fatal("same day must breach")
	}
	if CheckDailyDeposit(lim, c, 1, early) != nil {
		t.Fatal("next BRT day must not breach")
	}
}
