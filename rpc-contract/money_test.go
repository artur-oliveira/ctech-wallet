package rpccontract

import (
	"encoding/json"
	"os"
	"testing"
)

// TestMoneyConstantsMatchJSON pins the Go consts to money.json, the canonical
// cross-language source (the ui has the mirror test for its side).
func TestMoneyConstantsMatchJSON(t *testing.T) {
	raw, err := os.ReadFile("money.json")
	if err != nil {
		t.Fatalf("reading money.json: %v", err)
	}
	var m struct {
		FeeBps                   int64 `json:"fee_bps"`
		FeeMin                   int64 `json:"fee_min"`
		FeeMax                   int64 `json:"fee_max"`
		FeeAbsoluteMin           int64 `json:"fee_absolute_min"`
		SandboxCreditsPerCentavo int64 `json:"sandbox_credits_per_centavo"`
		MaxAmountCents           int64 `json:"max_amount_cents"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parsing money.json: %v", err)
	}

	checks := []struct {
		name string
		got  int64
		want int64
	}{
		{"fee_bps", DefaultFeeBps, m.FeeBps},
		{"fee_min", DefaultFeeMin, m.FeeMin},
		{"fee_max", DefaultFeeMax, m.FeeMax},
		{"fee_absolute_min", AbsoluteFeeMin, m.FeeAbsoluteMin},
		{"sandbox_credits_per_centavo", SandboxCreditsPerCentavo, m.SandboxCreditsPerCentavo},
		{"max_amount_cents", MaxAmountCents, m.MaxAmountCents},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: Go const %d != money.json %d", c.name, c.got, c.want)
		}
	}
}
