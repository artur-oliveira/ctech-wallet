package v1

import (
	"testing"

	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
)

func TestWalletBalancesResponseExposesSandboxHistoryWithoutActivation(t *testing.T) {
	realw := &wallet.Wallet{WalletID: "w-real", Type: wallet.TypeReal}
	sandboxw := &wallet.Wallet{WalletID: "w-sandbox", Type: wallet.TypeSandbox}

	out := walletBalancesResponse(realw, nil, sandboxw, "")

	if activated, ok := out["activated"].(bool); !ok || activated {
		t.Fatalf("activated = %#v, want false without a game wallet", out["activated"])
	}
	if got := out["sandbox"]; got != sandboxw {
		t.Fatalf("sandbox = %#v, want existing sandbox wallet for read-only history", got)
	}
	if _, exists := out["game"]; exists {
		t.Fatal("game must stay absent before gambling activation")
	}
}

func TestWalletBalancesResponseDoesNotInventSandbox(t *testing.T) {
	realw := &wallet.Wallet{WalletID: "w-real", Type: wallet.TypeReal}

	out := walletBalancesResponse(realw, nil, nil, "")

	if _, exists := out["sandbox"]; exists {
		t.Fatal("sandbox must stay absent when no sandbox wallet exists")
	}
}
