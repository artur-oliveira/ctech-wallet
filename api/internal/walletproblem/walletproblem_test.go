package walletproblem

import (
	"encoding/json"
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

// The wire format is a contract with the UI and with Poker: type, status and the
// extension members must not drift when the shared package changes.
func TestWireFormatPreserved(t *testing.T) {
	cases := []struct {
		name   string
		body   any
		status int
		typ    string
		extra  map[string]float64
	}{
		{"insufficient", InsufficientBalance(), http.StatusConflict, "/problems/insufficient-balance", nil},
		{"busy", WalletBusy(), http.StatusConflict, "/problems/wallet-busy", nil},
		{"idempotency", IdempotencyConflict(), http.StatusConflict, "/problems/idempotency-conflict", nil},
		{"step-up", StepUpRequired(300), http.StatusForbidden, "/problems/step-up-required", map[string]float64{"max_age_seconds": 300}},
		{"retired", RouteRetired(), http.StatusGone, "/problems/route-retired", nil},
		{"range", DepositOutOfRange(100, 1000000), http.StatusUnprocessableEntity, "/problems/deposit-out-of-range",
			map[string]float64{"min_amount": 100, "max_amount": 1000000}},
	}
	for _, tc := range cases {
		b, err := json.Marshal(tc.body)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		if m["type"] != tc.typ || int(m["status"].(float64)) != tc.status {
			t.Errorf("%s: type/status drifted: %s", tc.name, b)
		}
		if _, ok := m["code"]; ok {
			t.Errorf("%s: unexpected code member: %s", tc.name, b)
		}
		for k, v := range tc.extra {
			if m[k] != v {
				t.Errorf("%s: %s = %v, want %v", tc.name, k, m[k], v)
			}
		}
	}
}
