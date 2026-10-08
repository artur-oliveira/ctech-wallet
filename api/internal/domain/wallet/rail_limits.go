package wallet

import "time"

// DepositRailLimits is what the deposit dialog may offer right now. Amounts are
// centavos. MaxNow already folds in today's headroom, so a client never computes
// money: it shows Min..MaxNow (and disables the action when MaxNow is 0).
type DepositRailLimits struct {
	Min            int64 `json:"min"`
	Max            int64 `json:"max"` // per-deposit range ceiling
	DailyCap       int64 `json:"daily_cap"`
	DailyRemaining int64 `json:"daily_remaining"`
	MaxNow         int64 `json:"max_now"` // 0 means a deposit is not possible now
}

// WithdrawRailLimits is the same for the withdrawal dialog.
type WithdrawRailLimits struct {
	Min            int64 `json:"min"`
	DailyCap       int64 `json:"daily_cap"`
	DailyRemaining int64 `json:"daily_remaining"`
	CountRemaining int64 `json:"count_remaining"`
	MaxNow         int64 `json:"max_now"` // 0 means a withdrawal is not possible now
}

// RailLimits is the effective PIX limit view of the real wallet, as of now.
type RailLimits struct {
	Deposit  DepositRailLimits  `json:"deposit"`
	Withdraw WithdrawRailLimits `json:"withdraw"`
	ResetsAt string             `json:"resets_at"` // RFC3339, when the daily counters roll
}

// ComputeRailLimits combines the wallet's per-deposit range, its daily limits and
// today's counters into what is allowed right now. Display-only: the enforcement
// stays in InitiateDeposit, ConfirmDeposit and Withdraw.
func ComputeRailLimits(w *Wallet, c RealDailyCounters, now time.Time) RailLimits {
	day, _, _ := WindowKeys(now)
	used := c.ForDay(day)
	daily := EffectiveDailyLimits(w)
	minDep, maxDep := DepositLimits(w)
	reset, _, _ := WindowResets(now)

	depRemaining := max(0, daily.DepositCap-used.DepositSum)
	depNow := min(maxDep, depRemaining)
	if depNow < minDep {
		depNow = 0 // the room left is below the minimum deposit
	}

	wdRemaining := max(0, daily.WithdrawCap-used.WithdrawSum)
	countRemaining := max(0, daily.WithdrawCount-used.WithdrawCount)
	minWd := MinWithdrawal(w)
	var balance int64
	if w != nil {
		balance = w.Balance
	}
	wdNow := min(balance, wdRemaining)
	// Below the minimum is allowed only when it empties the whole balance.
	if countRemaining == 0 || wdNow <= 0 || (wdNow < minWd && wdNow != balance) {
		wdNow = 0
	}

	return RailLimits{
		Deposit: DepositRailLimits{
			Min: minDep, Max: maxDep, DailyCap: daily.DepositCap, DailyRemaining: depRemaining, MaxNow: depNow,
		},
		Withdraw: WithdrawRailLimits{
			Min: minWd, DailyCap: daily.WithdrawCap, DailyRemaining: wdRemaining, CountRemaining: countRemaining, MaxNow: wdNow,
		},
		ResetsAt: reset.Format(time.RFC3339),
	}
}
