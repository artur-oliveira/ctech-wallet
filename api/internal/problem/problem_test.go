package problem

import (
	"net/http"
	"testing"
	"time"
)

func TestDailyLimitProblems(t *testing.T) {
	reset := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	d := DailyDepositLimit(100000, 60000, reset)
	if d.Status != http.StatusConflict || d.Type != TypeDailyDepositLimit || d.MaxAmount != 100000 {
		t.Fatalf("deposit problem: %+v", d)
	}
	w := DailyWithdrawLimit("withdraw_count", 1, 1, reset)
	if w.Status != http.StatusConflict || w.Type != TypeDailyWithdrawLimit {
		t.Fatalf("withdraw problem: %+v", w)
	}
}
