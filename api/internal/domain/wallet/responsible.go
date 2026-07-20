package wallet

import (
	"fmt"
	"time"
)

// saoPaulo is the calendar for every responsible-gambling window: users reason
// in their own local day/week/month, not UTC.
var saoPaulo = mustLoadSP()

func mustLoadSP() *time.Location {
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		panic("America/Sao_Paulo tz missing — import _ \"time/tzdata\" in main: " + err.Error())
	}
	return loc
}

// SelfExclusion records a user's self-exclusion. Fixed periods carry Until;
// indefinite has Until == "" and never expires on its own.
type SelfExclusion struct {
	Period      string `dynamodbav:"period" json:"period"` // "30d" | "90d" | "indefinite"
	RequestedAt string `dynamodbav:"requested_at" json:"requested_at"`
	Until       string `dynamodbav:"until,omitempty" json:"until,omitempty"`
}

// GameLimits are the user-set deposit caps in centavos. Zero = not configured.
type GameLimits struct {
	Daily   int64          `dynamodbav:"daily" json:"daily"`
	Weekly  int64          `dynamodbav:"weekly" json:"weekly"`
	Monthly int64          `dynamodbav:"monthly" json:"monthly"`
	Pending *PendingLimits `dynamodbav:"pending,omitempty" json:"pending,omitempty"`
}

// PendingLimits is a scheduled increase waiting out its cooldown.
type PendingLimits struct {
	Daily     int64  `dynamodbav:"daily" json:"daily"`
	Weekly    int64  `dynamodbav:"weekly" json:"weekly"`
	Monthly   int64  `dynamodbav:"monthly" json:"monthly"`
	AppliesAt string `dynamodbav:"applies_at" json:"applies_at"`
}

// GameDepositCounters accumulate real→game deposits per calendar window. A key
// mismatch means the window rolled and the sum is logically zero.
type GameDepositCounters struct {
	DayKey   string `dynamodbav:"day_key,omitempty" json:"day_key,omitempty"`
	DaySum   int64  `dynamodbav:"day_sum,omitempty" json:"day_sum,omitempty"`
	WeekKey  string `dynamodbav:"week_key,omitempty" json:"week_key,omitempty"`
	WeekSum  int64  `dynamodbav:"week_sum,omitempty" json:"week_sum,omitempty"`
	MonthKey string `dynamodbav:"month_key,omitempty" json:"month_key,omitempty"`
	MonthSum int64  `dynamodbav:"month_sum,omitempty" json:"month_sum,omitempty"`
}

// LimitBreach describes which window a deposit would overflow.
type LimitBreach struct {
	Window   string // "daily" | "weekly" | "monthly"
	Limit    int64
	Used     int64
	ResetsAt time.Time
}

// WindowKeys returns the São Paulo calendar keys for now.
func WindowKeys(now time.Time) (day, week, month string) {
	t := now.In(saoPaulo)
	y, w := t.ISOWeek()
	return t.Format("2006-01-02"), fmt.Sprintf("%04d-W%02d", y, w), t.Format("2006-01")
}

// WindowResets returns when each window rolls over (São Paulo calendar).
func WindowResets(now time.Time) (day, week, month time.Time) {
	t := now.In(saoPaulo)
	midnight := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, saoPaulo)
	day = midnight.AddDate(0, 0, 1)
	daysToMonday := (8 - int(t.Weekday())) % 7
	if daysToMonday == 0 {
		daysToMonday = 7
	}
	week = midnight.AddDate(0, 0, daysToMonday)
	month = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, saoPaulo).AddDate(0, 1, 0)
	return day, week, month
}

// SumsFor returns the counters' sums for the given keys, zeroing any window
// whose key rolled.
func (c GameDepositCounters) SumsFor(day, week, month string) (d, w, m int64) {
	if c.DayKey == day {
		d = c.DaySum
	}
	if c.WeekKey == week {
		w = c.WeekSum
	}
	if c.MonthKey == month {
		m = c.MonthSum
	}
	return d, w, m
}

// CheckDeposit reports the first window `amount` would overflow, or nil.
func CheckDeposit(limits GameLimits, c GameDepositCounters, amount int64, now time.Time) *LimitBreach {
	day, week, month := WindowKeys(now)
	d, w, m := c.SumsFor(day, week, month)
	dr, wr, mr := WindowResets(now)
	// Use `amount > limit - used` rather than `used + amount > limit`: the latter
	// can wrap past math.MaxInt64 on huge sums and turn a real breach into a false
	// negative, defeating the limit (SEC-04). With this form a negative `limit -
	// used` (counter drifted above the cap) still trips the breach.
	switch {
	case amount > limits.Daily-d:
		return &LimitBreach{Window: "daily", Limit: limits.Daily, Used: d, ResetsAt: dr}
	case amount > limits.Weekly-w:
		return &LimitBreach{Window: "weekly", Limit: limits.Weekly, Used: w, ResetsAt: wr}
	case amount > limits.Monthly-m:
		return &LimitBreach{Window: "monthly", Limit: limits.Monthly, Used: m, ResetsAt: mr}
	}
	return nil
}

// SelfExcluded reports whether the user is currently excluded. Fixed periods
// expire lazily; indefinite never does. An unparseable Until fails closed.
func (u *User) SelfExcluded(now time.Time) bool {
	if u == nil || u.SelfExclusion == nil {
		return false
	}
	if u.SelfExclusion.Until == "" {
		return true // indefinite
	}
	until, err := time.Parse(time.RFC3339, u.SelfExclusion.Until)
	return err != nil || now.Before(until)
}

// LimitsConfigured reports whether all three limits are set.
func (u *User) LimitsConfigured() bool {
	return u != nil && u.GameLimits != nil &&
		u.GameLimits.Daily > 0 && u.GameLimits.Weekly > 0 && u.GameLimits.Monthly > 0
}

// EffectiveGameLimits returns the limits with a matured pending increase
// applied. `applied` tells the caller to persist the promotion (lazy apply).
func (u *User) EffectiveGameLimits(now time.Time) (GameLimits, bool) {
	if u == nil || u.GameLimits == nil {
		return GameLimits{}, false
	}
	lim := *u.GameLimits
	if p := lim.Pending; p != nil {
		at, err := time.Parse(time.RFC3339, p.AppliesAt)
		if err == nil && !now.Before(at) {
			return GameLimits{Daily: p.Daily, Weekly: p.Weekly, Monthly: p.Monthly}, true
		}
	}
	return lim, false
}

// ValidateLimits enforces 0 < daily ≤ weekly ≤ monthly.
func ValidateLimits(daily, weekly, monthly int64) error {
	if daily <= 0 || weekly < daily || monthly < weekly {
		return fmt.Errorf("limits must satisfy 0 < daily <= weekly <= monthly")
	}
	return nil
}

// ExclusionUntil resolves a period string to its expiry (empty = indefinite).
func ExclusionUntil(period string, now time.Time) (string, error) {
	switch period {
	case "30d":
		return now.Add(30 * 24 * time.Hour).Format(time.RFC3339), nil
	case "90d":
		return now.Add(90 * 24 * time.Hour).Format(time.RFC3339), nil
	case "indefinite":
		return "", nil
	default:
		return "", fmt.Errorf("unknown exclusion period %q", period)
	}
}
