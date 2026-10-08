package wallet

import "time"

// Default daily PIX limits for the real wallet, in centavos. A wallet may
// override any of them via admin-only DynamoDB fields; zero means "default".
const (
	DefaultDailyDepositCap    int64 = 100_000 // R$ 1.000,00 deposited per day
	DefaultDailyWithdrawCap   int64 = 100_000 // R$ 1.000,00 withdrawn per day
	DefaultDailyWithdrawCount int64 = 1       // withdrawals per day
)

// Breach kinds reported by CheckDaily*.
const (
	DailyBreachDepositCap    = "deposit_cap"
	DailyBreachWithdrawCap   = "withdraw_cap"
	DailyBreachWithdrawCount = "withdraw_count"
)

// DailyLimits is the effective per-wallet daily PIX limit set.
type DailyLimits struct {
	DepositCap    int64
	WithdrawCap   int64
	WithdrawCount int64
}

// EffectiveDailyLimits applies the per-wallet overrides over the defaults.
// Pass w == nil for the defaults.
func EffectiveDailyLimits(w *Wallet) DailyLimits {
	lim := DailyLimits{
		DepositCap:    DefaultDailyDepositCap,
		WithdrawCap:   DefaultDailyWithdrawCap,
		WithdrawCount: DefaultDailyWithdrawCount,
	}
	if w == nil {
		return lim
	}
	if w.DailyDepositCap > 0 {
		lim.DepositCap = w.DailyDepositCap
	}
	if w.DailyWithdrawCap > 0 {
		lim.WithdrawCap = w.DailyWithdrawCap
	}
	if w.DailyWithdrawCount > 0 {
		lim.WithdrawCount = w.DailyWithdrawCount
	}
	return lim
}

// RealDailyCounters accumulate the real wallet's PIX activity for one São Paulo
// calendar day. A DayKey mismatch means the day rolled and every sum is
// logically zero. Stored on the user row (like GameDepositCounters) and only
// ever written under the real wallet lock.
type RealDailyCounters struct {
	DayKey        string `dynamodbav:"day_key,omitempty" json:"day_key,omitempty"`
	DepositSum    int64  `dynamodbav:"deposit_sum,omitempty" json:"deposit_sum,omitempty"`
	WithdrawSum   int64  `dynamodbav:"withdraw_sum,omitempty" json:"withdraw_sum,omitempty"`
	WithdrawCount int64  `dynamodbav:"withdraw_count,omitempty" json:"withdraw_count,omitempty"`
}

// ForDay returns the counters as they stand for day: unchanged when the key
// matches, zeroed (with the new key) when the day rolled.
func (c RealDailyCounters) ForDay(day string) RealDailyCounters {
	if c.DayKey != day {
		return RealDailyCounters{DayKey: day}
	}
	return c
}

// DailyBreach describes which daily limit an operation would overflow.
type DailyBreach struct {
	Kind     string
	Limit    int64
	Used     int64
	ResetsAt time.Time
}

// CheckDailyDeposit reports whether crediting amount would overflow the daily
// deposit cap. Uses `amount > cap-used` rather than `used+amount > cap` so huge
// sums cannot wrap past math.MaxInt64 into a false negative (SEC-04).
func CheckDailyDeposit(lim DailyLimits, c RealDailyCounters, amount int64, now time.Time) *DailyBreach {
	day, _, _ := WindowKeys(now)
	used := c.ForDay(day).DepositSum
	if amount > lim.DepositCap-used {
		reset, _, _ := WindowResets(now)
		return &DailyBreach{Kind: DailyBreachDepositCap, Limit: lim.DepositCap, Used: used, ResetsAt: reset}
	}
	return nil
}

// CheckDailyWithdraw reports whether a withdrawal of amount would exceed the
// daily withdrawal count or amount cap. The count is checked first.
func CheckDailyWithdraw(lim DailyLimits, c RealDailyCounters, amount int64, now time.Time) *DailyBreach {
	day, _, _ := WindowKeys(now)
	cur := c.ForDay(day)
	reset, _, _ := WindowResets(now)
	if cur.WithdrawCount >= lim.WithdrawCount {
		return &DailyBreach{Kind: DailyBreachWithdrawCount, Limit: lim.WithdrawCount, Used: cur.WithdrawCount, ResetsAt: reset}
	}
	if amount > lim.WithdrawCap-cur.WithdrawSum {
		return &DailyBreach{Kind: DailyBreachWithdrawCap, Limit: lim.WithdrawCap, Used: cur.WithdrawSum, ResetsAt: reset}
	}
	return nil
}
