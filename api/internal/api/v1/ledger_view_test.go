package v1

import (
	"encoding/json"
	"testing"

	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
)

func TestLedgerEntryViewBalanceBefore(t *testing.T) {
	cases := []struct {
		name          string
		amount, after int64
		wantBefore    int64
	}{
		{"credit", 5000, 15000, 10000},
		{"debit", -3000, 7000, 10000},
		{"first credit on empty wallet", 1000, 1000, 0},
		{"debit to zero", -500, 0, 500},
	}
	for _, tc := range cases {
		v := newLedgerEntryView(wallet.LedgerEntry{Amount: tc.amount, BalanceAfter: tc.after})
		if v.BalanceBefore != tc.wantBefore {
			t.Errorf("%s: before = %d, want %d", tc.name, v.BalanceBefore, tc.wantBefore)
		}
	}
}

func TestLedgerEntryViewJSONShape(t *testing.T) {
	v := newLedgerEntryView(wallet.LedgerEntry{
		WalletID: "w1", SK: "sk-secret", EntryID: "e1", Type: "deposit", Amount: 100, BalanceAfter: 300,
		IdempotencyKey: "idem-secret", Ref: "tx1", Description: "Depósito PIX", CreatedAt: "2026-10-08T12:00:00Z",
	})
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"wallet_id", "entry_id", "type", "amount", "balance_after", "balance_before", "ref", "description", "created_at"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing key %q in %s", k, raw)
		}
	}
	for _, k := range []string{"pk", "sk", "idempotency_key"} {
		if _, ok := m[k]; ok {
			t.Errorf("internal key %q leaked: %s", k, raw)
		}
	}
	if m["balance_before"].(float64) != 200 {
		t.Errorf("balance_before = %v", m["balance_before"])
	}
}
