//go:build integration

package integration_test

import (
	"context"
	"testing"

	"gopkg.aoctech.app/wallet/api/internal/domain/id"
	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/walletproblem"
)

// The successors of the ring-fence regression tests (the game wallet, the Sandbox purchase from it and
// the gambling activation are gone). The guarantee they held is the one that matters most and must never
// lapse: real money never becomes Sandbox. Sandbox has no way to debit `real`, and real has none to
// credit Sandbox, whatever else changes around them.

// TestSandboxHasNoRealDebitPath: spending Sandbox, even with a large real balance and an empty Sandbox,
// can only fail; it never reaches into `real`.
func TestSandboxHasNoRealDebitPath(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()
	fund(t, h, user, 10000)

	_, err := h.svc.DebitSandbox(ctx, user, 1000, "idem-sbx-debit", "bet", "")
	wantProblem(t, err, walletproblem.TypeInsufficientBalance)

	real, _, sandbox, err := h.repo.LoadWallets(ctx, user)
	if err != nil {
		t.Fatalf("LoadWallets: %v", err)
	}
	if real.Balance != 10000 {
		t.Fatalf("BYPASS: a Sandbox debit touched the real wallet (%d, want 10000)", real.Balance)
	}
	if sandbox != nil && sandbox.Balance != 0 {
		t.Fatalf("a failed Sandbox debit moved money: sandbox=%d", sandbox.Balance)
	}
}

// TestCreditingSandboxNeverDebitsReal: a Sandbox credit (what a pack sale does, with Billing paid by Pix)
// adds credits and takes nothing from `real`.
func TestCreditingSandboxNeverDebitsReal(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()
	fund(t, h, user, 10000)

	if _, err := h.svc.CreditSandbox(ctx, user, 5000, "idem-sbx-credit", "pack", ""); err != nil {
		t.Fatalf("CreditSandbox: %v", err)
	}
	real, _, sandbox, err := h.repo.LoadWallets(ctx, user)
	if err != nil {
		t.Fatalf("LoadWallets: %v", err)
	}
	if real.Balance != 10000 {
		t.Fatalf("BYPASS: a Sandbox credit debited the real wallet (%d, want 10000)", real.Balance)
	}
	if sandbox == nil || sandbox.Balance != 5000 {
		t.Fatalf("sandbox = %+v, want 5000", sandbox)
	}
}

// TestDebitingRealNeverCreditsSandbox: the other direction. Spending real (a table fee, a product) does not
// mint Sandbox credits.
func TestDebitingRealNeverCreditsSandbox(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()
	fund(t, h, user, 10000)

	if _, err := h.svc.DebitReal(ctx, user, 3000, "idem-real-debit", "fee", ""); err != nil {
		t.Fatalf("DebitReal: %v", err)
	}
	real, _, sandbox, err := h.repo.LoadWallets(ctx, user)
	if err != nil {
		t.Fatalf("LoadWallets: %v", err)
	}
	if real.Balance != 7000 {
		t.Fatalf("real = %d, want 7000", real.Balance)
	}
	if sandbox != nil && sandbox.Balance != 0 {
		t.Fatalf("BYPASS: a real debit credited Sandbox (%d)", sandbox.Balance)
	}
}

// TestAnOldGameWalletIsReadOnly: the retired game wallet is not reachable by any live operation, and the
// balances a caller sees do not show it.
func TestBalancesNeverShowARetiredGameWallet(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()
	fund(t, h, user, 100)

	real, game, _, err := h.svc.GetBalances(ctx, user)
	if err != nil {
		t.Fatalf("GetBalances: %v", err)
	}
	if real == nil {
		t.Fatal("real wallet must always exist")
	}
	if game != nil && game.RetiredAt == "" {
		t.Fatalf("a live game wallet appeared: %+v", game)
	}
	_ = wallet.TypeReal
}

// TestBalancesForReportsRealAsWhatAGameCanStake: Poker reads one number to know how much a player can
// bring to a table, and it is the real balance (the game wallet is gone), next to the Sandbox balance.
func TestBalancesForReportsRealAsWhatAGameCanStake(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()
	fund(t, h, user, 7000)
	if _, err := h.svc.CreditSandbox(ctx, user, 2500, "idem-sbx", "pack", ""); err != nil {
		t.Fatalf("CreditSandbox: %v", err)
	}

	b, err := h.svc.BalancesFor(ctx, user)
	if err != nil {
		t.Fatalf("BalancesFor: %v", err)
	}
	if b.GameBalance != 7000 || b.SandboxBalance != 2500 {
		t.Fatalf("balances = %+v, want game=7000 (the real balance) sandbox=2500", b)
	}
}
