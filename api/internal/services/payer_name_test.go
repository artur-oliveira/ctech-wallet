package services

import "testing"

func TestPayerNameMatches(t *testing.T) {
	cases := []struct {
		payer, kyc string
		want       bool
	}{
		{"Artur Oliveira Carvalho", "Artur Oliveira Carvalho", true},
		{"ARTUR OLIVEIRA CARVALHO", "artur oliveira carvalho", true},
		{"JOSÉ DA SILVA SANTOS", "Jose Silva Santos", true}, // accents and connectors
		{"Artur Carvalho", "Artur Oliveira Carvalho", true}, // a middle name left out
		{"Maria de Souza", "Maria Souza", true},
		{"Artur Oliveira Carvalho", "Arthur Oliveira Carvalho", false}, // another first name
		{"Artur Oliveira Silva", "Artur Oliveira Carvalho", false},     // another last name
		{"Artur", "Artur Carvalho", false},                             // one word against two
		{"Artur", "Artur", true},
		{"", "Artur Carvalho", false},
		{"Artur Carvalho", "", false},
		{"A. O. Carvalho", "Artur Oliveira Carvalho", false}, // initials are not a name
	}
	for _, c := range cases {
		if got := payerNameMatches(c.payer, c.kyc); got != c.want {
			t.Errorf("%q vs %q: %v, want %v", c.payer, c.kyc, got, c.want)
		}
	}
}
