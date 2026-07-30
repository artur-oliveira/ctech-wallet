package asaas

import "testing"

func TestCentavosReaisRoundTrip(t *testing.T) {
	cases := []int64{0, 1, 100, 12345, 999999}
	for _, c := range cases {
		if got := reaisToCentavos(centavosToReais(c)); got != c {
			t.Errorf("round trip %d: got %d", c, got)
		}
	}
}

func TestReaisToCentavosRounds(t *testing.T) {
	if got := reaisToCentavos(19.9); got != 1990 {
		t.Errorf("got %d, want 1990", got)
	}
	if got := reaisToCentavos(0.1 + 0.2); got != 30 {
		t.Errorf("float imprecision not rounded away: got %d, want 30", got)
	}
}
