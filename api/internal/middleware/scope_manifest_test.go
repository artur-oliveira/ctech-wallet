package middleware

import (
	"sort"
	"testing"

	"gopkg.aoctech.app/wallet/api/internal/oauthresource"
)

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
		ScopePixConfirmDeposit, ScopeWalletGameHold, ScopeWalletGameCashout,
		ScopeWalletGameStatus, ScopeWalletBalance, ScopeWalletSandboxPurchase,
		ScopeWalletProductPurchase,
	}
	wantPublic := WalletPublicScopes()
	gotInternal := make([]string, 0, len(wantInternal))
	gotPublic := make([]string, 0, len(wantPublic))
	for _, scope := range m.Scopes {
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
