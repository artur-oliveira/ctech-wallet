package wallet

import "testing"

func TestToSandboxCredits(t *testing.T) {
	// R$ 1,00 (100 centavos) → 1000 credits at the fixed rate of 10 credits/centavo.
	cases := []struct {
		centavos int64
		want     int64
	}{
		{0, 0},
		{1, 10},
		{100, 1000},       // R$ 1,00
		{250, 2500},       // R$ 2,50
		{10000, 100_000},  // R$ 100,00
	}
	for _, c := range cases {
		if got := ToSandboxCredits(c.centavos); got != c.want {
			t.Errorf("ToSandboxCredits(%d) = %d, want %d", c.centavos, got, c.want)
		}
	}
}
