package wallet

import (
	"encoding/json"
	"os"
	"testing"
)

func TestToSandboxCredits(t *testing.T) {
	// R$ 1,00 (100 centavos) → 1000 credits at the fixed rate of 10 credits/centavo.
	cases := []struct {
		centavos int64
		want     int64
	}{
		{0, 0},
		{1, 100},
		{100, 10000},       // R$ 1,00
		{250, 25000},       // R$ 2,50
		{10000, 1_000_000}, // R$ 100,00
	}
	for _, c := range cases {
		if got := ToSandboxCredits(c.centavos); got != c.want {
			t.Errorf("ToSandboxCredits(%d) = %d, want %d", c.centavos, got, c.want)
		}
	}
}

// The money constants are mirrored from rpc-contract/money.json, the cross-language source the UI
// pins too (ui/src/lib/utils/money-contract.test.mjs). A drift fails here.
func TestMoneyConstantsMatchJSON(t *testing.T) {
	raw, err := os.ReadFile("../../../../rpc-contract/money.json")
	if err != nil {
		t.Fatalf("reading money.json: %v", err)
	}
	var m struct {
		SandboxCreditsPerCent int64 `json:"sandbox_credits_per_centavo"`
		MaxAmountCents        int64 `json:"max_amount_cents"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parsing money.json: %v", err)
	}
	if SandboxCreditsPerCent != m.SandboxCreditsPerCent {
		t.Errorf("SandboxCreditsPerCent %d != money.json %d", SandboxCreditsPerCent, m.SandboxCreditsPerCent)
	}
	if MaxInboundAmount != m.MaxAmountCents {
		t.Errorf("MaxInboundAmount %d != money.json %d", MaxInboundAmount, m.MaxAmountCents)
	}
}
