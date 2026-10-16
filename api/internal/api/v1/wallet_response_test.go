package v1

import (
	"testing"

	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
)

func TestWalletBalancesResponseExposesSandboxHistory(t *testing.T) {
	realw := &wallet.Wallet{WalletID: "w-real", Type: wallet.TypeReal}
	sandboxw := &wallet.Wallet{WalletID: "w-sandbox", Type: wallet.TypeSandbox}

	out := walletBalancesResponse(realw, nil, sandboxw)

	if _, exists := out["activated"]; exists {
		t.Fatal("activation is gone: gambling consent is Poker's")
	}
	if got := out["sandbox"]; got != sandboxw {
		t.Fatalf("sandbox = %#v, want existing sandbox wallet for read-only history", got)
	}
	if _, exists := out["game"]; exists {
		t.Fatal("a nil game wallet must stay absent")
	}
}

func TestWalletBalancesResponseDoesNotInventSandbox(t *testing.T) {
	realw := &wallet.Wallet{WalletID: "w-real", Type: wallet.TypeReal}

	out := walletBalancesResponse(realw, nil, nil)

	if _, exists := out["sandbox"]; exists {
		t.Fatal("sandbox must stay absent when no sandbox wallet exists")
	}
}
