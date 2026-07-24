package wallet

import "testing"

func TestGamblingAcceptedRequiresCurrentVersion(t *testing.T) {
	cases := []struct {
		name string
		u    *User
		want bool
	}{
		{"nil user", nil, false},
		{"never accepted", &User{}, false},
		{"stale version", &User{GamblingAddendumVersion: "0.9"}, false},
		{"current version", &User{GamblingAddendumVersion: CurrentGamblingAddendumVersion}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.u.GamblingAccepted(); got != tc.want {
				t.Errorf("GamblingAccepted() = %v, want %v", got, tc.want)
			}
		})
	}
}

// The gambling addendum is a document distinct from the wallet terms addendum;
// accepting one must never imply the other.
func TestGamblingAddendumIsIndependentOfTermsAddendum(t *testing.T) {
	u := &User{TermsAddendumVersion: CurrentTermsAddendumVersion}
	if u.GamblingAccepted() {
		t.Error("accepting the terms addendum must not grant gambling acceptance")
	}
	g := &User{GamblingAddendumVersion: CurrentGamblingAddendumVersion}
	if g.TermsAccepted() {
		t.Error("accepting the gambling addendum must not grant terms acceptance")
	}
}
