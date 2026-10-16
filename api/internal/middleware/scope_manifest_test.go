package middleware

import (
	"sort"
	"testing"

	"gopkg.aoctech.app/wallet/api/internal/oauthresource"
)

// retiredScopes were removed from the code and stay in the manifest as deprecated: the
// registry (ctech-account) refuses to drop an active scope before it was published as
// deprecated. custody went with the Asaas BaaS integration; the others with the Pix gateway,
// the charge route and the sale of Sandbox packs and goods (wallet ledger P5).
var retiredScopes = map[string]bool{
	"wallet:custody:write":               true,
	"wallet:sandbox-purchases:read":      true,
	"wallet:sandbox-purchases:write":     true,
	"wallet:product-purchases:read":      true,
	"internal:wallet:confirm-deposit":    true,
	"internal:wallet:sandbox-purchase":   true,
	"internal:wallet:product-purchase":   true,
	"internal:wallet:charge-amount":      true,
	"wallet:game:write":                  true,
	"wallet:gambling:read":               true,
	"wallet:gambling:write":              true,
	"internal:wallet:game-hold":          true,
	"internal:wallet:game-cashout":       true,
	"internal:wallet:game-status":        true,
	"internal:wallet:responsible-export": true,
}

func TestScopeManifestMatchesEnforcedScopes(t *testing.T) {
	m, err := oauthresource.ManifestDocument()
	if err != nil {
		t.Fatal(err)
	}
	if m.ResourceServerID != "wallet" || m.SchemaVersion != 1 {
		t.Fatalf("unexpected manifest identity: %#v", m)
	}
	wantInternal := []string{
		ScopeWalletCredit, ScopeWalletDebit, ScopeWalletRealDebit,
		ScopeWalletBalance, ScopeWalletErasureEligibility,
		ScopeWalletHold,
	}
	wantPublic := WalletPublicScopes()
	gotInternal := make([]string, 0, len(wantInternal))
	gotPublic := make([]string, 0, len(wantPublic))
	for _, scope := range m.Scopes {
		// The registry (ctech-account) refuses to drop an active scope before
		// it was published as deprecated, so a retired scope stays here as
		// deprecated until a later change removes it. It is no longer enforced.
		if retiredScopes[scope.Name] {
			if scope.Status != "deprecated" {
				t.Fatalf("retired scope %q must stay deprecated", scope.Name)
			}
			continue
		}
		if scope.Status != "active" {
			t.Fatalf("Wallet scope %q must be active", scope.Name)
		}
		if scope.Descriptions["pt-BR"] == "" || scope.Descriptions["en"] == "" {
			t.Fatalf("Wallet scope %q must have pt-BR and en descriptions", scope.Name)
		}
		switch scope.Visibility {
		case "internal":
			gotInternal = append(gotInternal, scope.Name)
		case "public":
			gotPublic = append(gotPublic, scope.Name)
		default:
			t.Fatalf("Wallet scope %q has invalid visibility %q", scope.Name, scope.Visibility)
		}
	}
	assertScopesEqual(t, "internal", gotInternal, wantInternal)
	assertScopesEqual(t, "public", gotPublic, wantPublic)
}

func assertScopesEqual(t *testing.T, kind string, got, want []string) {
	t.Helper()
	sort.Strings(got)
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("%s manifest scopes=%v enforced scopes=%v", kind, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s manifest/enforcement drift: got=%v want=%v", kind, got, want)
		}
	}
}
