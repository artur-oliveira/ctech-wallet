package wallet

import (
	"math"
	"testing"
	"time"
)

func spTime(t *testing.T, s string) time.Time {
	t.Helper()
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := time.ParseInLocation("2006-01-02 15:04", s, loc)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func TestWindowKeysUseSaoPauloCalendar(t *testing.T) {
	// 2026-07-19 23:30 UTC is 2026-07-19 20:30 in São Paulo (UTC-3).
	utc := time.Date(2026, 7, 19, 23, 30, 0, 0, time.UTC)
	day, week, month := WindowKeys(utc)
	if day != "2026-07-19" || month != "2026-07" {
		t.Fatalf("got day=%s month=%s", day, month)
	}
	if week != "2026-W29" { // 2026-07-19 is a Sunday of ISO week 29
		t.Fatalf("got week=%s", week)
	}
	// 00:30 UTC next day is STILL 2026-07-19 in São Paulo.
	day2, _, _ := WindowKeys(time.Date(2026, 7, 20, 0, 30, 0, 0, time.UTC))
	if day2 != "2026-07-19" {
		t.Fatalf("UTC rollover leaked into SP day key: %s", day2)
	}
}

func TestWindowResets(t *testing.T) {
	now := spTime(t, "2026-07-19 12:00") // Sunday
	day, week, month := WindowResets(now)
	if day.Format("2006-01-02 15:04") != "2026-07-20 00:00" {
		t.Fatalf("day reset: %v", day)
	}
	if week.Format("2006-01-02") != "2026-07-20" { // next Monday
		t.Fatalf("week reset: %v", week)
	}
	if month.Format("2006-01-02") != "2026-08-01" {
		t.Fatalf("month reset: %v", month)
	}
	// From a Monday, the week reset is the FOLLOWING Monday, not today.
	mon := spTime(t, "2026-07-20 10:00")
	_, week2, _ := WindowResets(mon)
	if week2.Format("2006-01-02") != "2026-07-27" {
		t.Fatalf("week reset from Monday: %v", week2)
	}
}

func TestSelfExcluded(t *testing.T) {
	now := spTime(t, "2026-07-19 12:00")
	cases := []struct {
		name string
		ex   *SelfExclusion
		want bool
	}{
		{"nil", nil, false},
		{"active fixed", &SelfExclusion{Period: "30d", Until: now.Add(time.Hour).Format(time.RFC3339)}, true},
		{"expired fixed", &SelfExclusion{Period: "30d", Until: now.Add(-time.Hour).Format(time.RFC3339)}, false},
		{"indefinite", &SelfExclusion{Period: "indefinite"}, true},
		{"garbage until fails closed", &SelfExclusion{Period: "30d", Until: "not-a-time"}, true},
	}
	for _, c := range cases {
		u := &User{SelfExclusion: c.ex}
		if got := u.SelfExcluded(now); got != c.want {
			t.Fatalf("%s: got %v", c.name, got)
		}
	}
}

func TestEffectiveGameLimitsAppliesMaturedPending(t *testing.T) {
	now := spTime(t, "2026-07-19 12:00")
	u := &User{GameLimits: &GameLimits{Daily: 100, Weekly: 500, Monthly: 1000,
		Pending: &PendingLimits{Daily: 200, Weekly: 500, Monthly: 1000, AppliesAt: now.Add(-time.Minute).Format(time.RFC3339)}}}
	lim, applied := u.EffectiveGameLimits(now)
	if !applied || lim.Daily != 200 || lim.Pending != nil {
		t.Fatalf("matured pending not applied: %+v applied=%v", lim, applied)
	}
	u.GameLimits.Pending.AppliesAt = now.Add(time.Hour).Format(time.RFC3339)
	lim, applied = u.EffectiveGameLimits(now)
	if applied || lim.Daily != 100 {
		t.Fatalf("immature pending applied early: %+v", lim)
	}
}

func TestLimitsConfigured(t *testing.T) {
	if (&User{}).LimitsConfigured() {
		t.Fatal("no limits must not count as configured")
	}
	if (&User{GameLimits: &GameLimits{Daily: 1, Weekly: 2}}).LimitsConfigured() {
		t.Fatal("partial limits must not count as configured")
	}
	if !(&User{GameLimits: &GameLimits{Daily: 1, Weekly: 2, Monthly: 3}}).LimitsConfigured() {
		t.Fatal("full limits must count as configured")
	}
}

func TestCheckDeposit(t *testing.T) {
	now := spTime(t, "2026-07-19 12:00")
	day, week, month := WindowKeys(now)
	lim := GameLimits{Daily: 100, Weekly: 300, Monthly: 500}
	c := GameDepositCounters{DayKey: day, DaySum: 80, WeekKey: week, WeekSum: 80, MonthKey: month, MonthSum: 80}
	if b := CheckDeposit(lim, c, 20, now); b != nil {
		t.Fatalf("exactly-at-limit must pass: %+v", b)
	}
	if b := CheckDeposit(lim, c, 21, now); b == nil || b.Window != "daily" {
		t.Fatalf("expected daily breach, got %+v", b)
	}
	// Stale day key = fresh day, but week still counts.
	c.DayKey = "2026-07-18"
	c.WeekSum = 290
	if b := CheckDeposit(lim, c, 20, now); b == nil || b.Window != "weekly" {
		t.Fatalf("expected weekly breach, got %+v", b)
	}
	c.WeekSum = 80
	c.MonthSum = 495
	if b := CheckDeposit(lim, c, 20, now); b == nil || b.Window != "monthly" {
		t.Fatalf("expected monthly breach, got %+v", b)
	}
}

func TestCheckDepositOverflow(t *testing.T) {
	// SEC-04: a near-int64-max running sum must still trip the breach. The old
	// `used + amount > limit` form wraps past math.MaxInt64 to a negative number
	// and silently lets the deposit through.
	now := spTime(t, "2026-07-19 12:00")
	day, week, month := WindowKeys(now)
	lim := GameLimits{Daily: 100, Weekly: 100, Monthly: 100}
	huge := int64(math.MaxInt64) - 10
	c := GameDepositCounters{DayKey: day, DaySum: huge, WeekKey: week, WeekSum: huge, MonthKey: month, MonthSum: huge}
	if b := CheckDeposit(lim, c, 50, now); b == nil {
		t.Fatal("expected breach for near-int64-max running sum")
	}
}

func TestValidateLimits(t *testing.T) {
	if err := ValidateLimits(100, 200, 300); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][3]int64{{0, 200, 300}, {100, 99, 300}, {100, 200, 199}} {
		if err := ValidateLimits(bad[0], bad[1], bad[2]); err == nil {
			t.Fatalf("expected error for %v", bad)
		}
	}
}

func TestExclusionUntil(t *testing.T) {
	now := spTime(t, "2026-07-19 12:00")
	until, err := ExclusionUntil("30d", now)
	if err != nil || until == "" {
		t.Fatalf("until=%q err=%v", until, err)
	}
	parsed, err := time.Parse(time.RFC3339, until)
	if err != nil || parsed.Sub(now) != 30*24*time.Hour {
		t.Fatalf("expected +30d, got %v err=%v", parsed, err)
	}
	if until2, _ := ExclusionUntil("indefinite", now); until2 != "" {
		t.Fatal("indefinite must have empty until")
	}
	if _, err := ExclusionUntil("7d", now); err == nil {
		t.Fatal("unknown period must error")
	}
}
