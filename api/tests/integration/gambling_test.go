//go:build integration

package integration_test

import (
	"context"
	"testing"

	"github.com/artur-oliveira/ctech-wallet/api/internal/domain/id"
	"github.com/artur-oliveira/ctech-wallet/api/internal/domain/wallet"
	"github.com/artur-oliveira/ctech-wallet/api/internal/kycclient"
	"github.com/artur-oliveira/ctech-wallet/api/internal/problem"
)

// Activation requires KYC `verified` — real money is about to enter a gambling
// ring-fence, so `basic` is not enough.
func TestActivateGamblingRequiresVerifiedKYC(t *testing.T) {
	ctx := context.Background()
	h := newHarness(&kycclient.KYC{Level: wallet.KYCBasic, CPF: cpf})
	user := "u-" + id.New()
	acceptGambling(t, h, user)

	_, _, err := h.svc.ActivateGambling(ctx, user, wallet.KYCBasic, "", "")
	wantProblem(t, err, problem.TypeKYCNotVerified)

	_, game, sandbox, err := h.repo.LoadWallets(ctx, user)
	if err != nil {
		t.Fatalf("LoadWallets: %v", err)
	}
	if game != nil || sandbox != nil {
		t.Fatal("a rejected activation must not create wallets")
	}
}

// Activation requires the gambling addendum — a separate document from the wallet
// terms addendum, so accepting the terms is not enough.
func TestActivateGamblingRequiresAddendum(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()
	if err := h.userRepo.AcceptTerms(ctx, user); err != nil {
		t.Fatalf("AcceptTerms: %v", err)
	}

	_, _, err := h.svc.ActivateGambling(ctx, user, wallet.KYCVerified, "", "")
	wantProblem(t, err, problem.TypeGamblingTermsRequired)
}

func TestActivateGamblingHappyPathIsIdempotentAndAudited(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()
	acceptGambling(t, h, user)

	game, sandbox, err := h.svc.ActivateGambling(ctx, user, wallet.KYCVerified, "203.0.113.7", "test-agent")
	if err != nil {
		t.Fatalf("ActivateGambling: %v", err)
	}
	if game.Type != wallet.TypeGame || sandbox.Type != wallet.TypeSandbox {
		t.Fatalf("types = %q/%q, want game/sandbox", game.Type, sandbox.Type)
	}

	// Idempotent: activating twice converges on the same wallets.
	game2, _, err := h.svc.ActivateGambling(ctx, user, wallet.KYCVerified, "", "")
	if err != nil {
		t.Fatalf("ActivateGambling replay: %v", err)
	}
	if game2.WalletID != game.WalletID {
		t.Fatalf("replay created a new game wallet: %s vs %s", game2.WalletID, game.WalletID)
	}

	events, err := h.audit.List(ctx, user, 10)
	if err != nil {
		t.Fatalf("audit List: %v", err)
	}
	activations := make([]wallet.AuditEvent, 0, 2)
	for _, e := range events {
		if e.EventType == wallet.EventGamblingActivated {
			activations = append(activations, e)
		}
	}
	// Exactly one — the replay must not forge a second activation record. The audit
	// log is evidence of what happened, and the user activated once.
	if len(activations) != 1 {
		t.Fatalf("gambling_activated events = %d, want exactly 1", len(activations))
	}
	if activations[0].IP != "203.0.113.7" || activations[0].UserAgent != "test-agent" {
		t.Errorf("audit row lost request context: ip=%q ua=%q",
			activations[0].IP, activations[0].UserAgent)
	}
}

// Before activation the balances response carries the real wallet only — a user
// who only pays for subscriptions never sees a gambling surface.
func TestGetBalancesHidesGamblingWalletsUntilActivated(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()

	real, game, sandbox, err := h.svc.GetBalances(ctx, user)
	if err != nil {
		t.Fatalf("GetBalances: %v", err)
	}
	if real == nil {
		t.Fatal("real wallet must always exist")
	}
	if game != nil || sandbox != nil {
		t.Fatal("game/sandbox must be nil before activation")
	}

	acceptGambling(t, h, user)
	if _, _, err := h.svc.ActivateGambling(ctx, user, wallet.KYCVerified, "", ""); err != nil {
		t.Fatalf("ActivateGambling: %v", err)
	}

	real, game, sandbox, err = h.svc.GetBalances(ctx, user)
	if err != nil {
		t.Fatalf("GetBalances after activation: %v", err)
	}
	if real == nil || game == nil || sandbox == nil {
		t.Fatal("after activation all three wallets must be present")
	}
}

// --- the real → game edge ---

func TestFundGameMovesRealMoneyIntoTheRingFence(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := fundedAndActivated(t, h, 10000)

	if _, _, err := h.svc.FundGame(ctx, user, 3000, "idem-1"); err != nil {
		t.Fatalf("FundGame: %v", err)
	}

	real, game, _, err := h.repo.LoadWallets(ctx, user)
	if err != nil {
		t.Fatalf("LoadWallets: %v", err)
	}
	if real.Balance != 7000 {
		t.Errorf("real = %d, want 7000", real.Balance)
	}
	if game.Balance != 3000 {
		t.Errorf("game = %d, want 3000", game.Balance)
	}

	// Idempotent replay: the same key must not move money twice.
	if _, _, err := h.svc.FundGame(ctx, user, 3000, "idem-1"); err != nil {
		t.Fatalf("FundGame replay: %v", err)
	}
	real, game, _, _ = h.repo.LoadWallets(ctx, user)
	if real.Balance != 7000 || game.Balance != 3000 {
		t.Fatalf("replay moved money again: real=%d game=%d", real.Balance, game.Balance)
	}
}

func TestFundGameCannotOverdrawReal(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := fundedAndActivated(t, h, 5000)

	_, _, err := h.svc.FundGame(ctx, user, 5001, "idem-over")
	wantProblem(t, err, problem.TypeInsufficientBalance)

	real, game, _, _ := h.repo.LoadWallets(ctx, user)
	if real.Balance != 5000 || game.Balance != 0 {
		t.Fatalf("failed fund must not move money: real=%d game=%d", real.Balance, game.Balance)
	}
}

func TestFundGameRequiresActivation(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()
	fund(t, h, user, 10000)

	_, _, err := h.svc.FundGame(ctx, user, 1000, "idem-x")
	wantProblem(t, err, problem.TypeGamblingNotActivated)
}

func TestReturnFromGameMovesMoneyBackAndIsNeverLimited(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := fundedAndActivated(t, h, 10000)

	if _, _, err := h.svc.FundGame(ctx, user, 4000, "idem-f"); err != nil {
		t.Fatalf("FundGame: %v", err)
	}
	if _, _, err := h.svc.ReturnFromGame(ctx, user, 4000, "idem-r"); err != nil {
		t.Fatalf("ReturnFromGame: %v", err)
	}

	real, game, _, err := h.repo.LoadWallets(ctx, user)
	if err != nil {
		t.Fatalf("LoadWallets: %v", err)
	}
	if real.Balance != 10000 || game.Balance != 0 {
		t.Fatalf("after round trip real=%d game=%d, want 10000/0", real.Balance, game.Balance)
	}
}

func TestReturnFromGameCannotOverdrawGame(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := fundedAndActivated(t, h, 10000)
	if _, _, err := h.svc.FundGame(ctx, user, 2000, "idem-f2"); err != nil {
		t.Fatalf("FundGame: %v", err)
	}

	_, _, err := h.svc.ReturnFromGame(ctx, user, 2001, "idem-r2")
	wantProblem(t, err, problem.TypeInsufficientBalance)
}

// --- the bypass ---

// THE BYPASS REGRESSION. Real money must never reach sandbox except across the
// real → game edge. If this ever fails, personal gambling limits are
// unenforceable: a user at their cap could buy sandbox straight from `real`.
func TestSandboxPurchaseNeverDebitsRealWallet(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := fundedAndActivated(t, h, 10000)

	if _, _, err := h.svc.FundGame(ctx, user, 4000, "idem-fund"); err != nil {
		t.Fatalf("FundGame: %v", err)
	}
	if _, _, err := h.svc.PurchaseSandbox(ctx, user, 1500, "idem-buy"); err != nil {
		t.Fatalf("PurchaseSandbox: %v", err)
	}

	real, game, sandbox, err := h.repo.LoadWallets(ctx, user)
	if err != nil {
		t.Fatalf("LoadWallets: %v", err)
	}
	// real is untouched by the purchase: 10000 - 4000 funded = 6000, not a centavo more.
	if real.Balance != 6000 {
		t.Errorf("real = %d, want 6000 — the purchase must NOT debit the real wallet", real.Balance)
	}
	if game.Balance != 2500 {
		t.Errorf("game = %d, want 2500 (4000 funded - 1500 spent)", game.Balance)
	}
	if sandbox.Balance != 1500 {
		t.Errorf("sandbox = %d, want 1500", sandbox.Balance)
	}
}

// A user with real money but an empty game wallet cannot buy sandbox at all: the
// real balance is simply not reachable from here.
func TestSandboxPurchaseCannotReachRealBalance(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := fundedAndActivated(t, h, 10000) // 10000 in REAL, 0 in game

	_, _, err := h.svc.PurchaseSandbox(ctx, user, 1000, "idem-bypass")
	wantProblem(t, err, problem.TypeInsufficientBalance)

	real, game, sandbox, _ := h.repo.LoadWallets(ctx, user)
	if real.Balance != 10000 {
		t.Fatalf("BYPASS: the purchase debited the real wallet (%d, want 10000)", real.Balance)
	}
	if game.Balance != 0 || sandbox.Balance != 0 {
		t.Fatalf("failed purchase moved money: game=%d sandbox=%d", game.Balance, sandbox.Balance)
	}
}

func TestSandboxPurchaseRequiresActivation(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()
	fund(t, h, user, 10000)

	_, _, err := h.svc.PurchaseSandbox(ctx, user, 1000, "idem-na")
	wantProblem(t, err, problem.TypeGamblingNotActivated)
}

// --- migration ---

// Users created under the old two-wallet model already have a sandbox wallet,
// sometimes with a balance. They never consented to a gambling addendum that did
// not exist, so they must NOT be treated as activated: the sandbox is frozen until
// they opt in. Nothing of value is trapped — sandbox holds no real money.
func TestLegacySandboxHolderIsNotTreatedAsActivated(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()
	fund(t, h, user, 10000)

	// A pre-migration row: real + sandbox, but no game wallet and no consent.
	byType, err := h.repo.EnsureWalletsOfType(ctx, user, wallet.TypeSandbox)
	if err != nil {
		t.Fatalf("EnsureWalletsOfType: %v", err)
	}
	legacy := byType[wallet.TypeSandbox]

	// Not activated: the sandbox is frozen.
	_, _, err = h.svc.PurchaseSandbox(ctx, user, 1000, "idem-legacy")
	wantProblem(t, err, problem.TypeGamblingNotActivated)

	_, err = h.svc.DebitSandbox(ctx, user, 100, "idem-legacy-d", "bet")
	wantProblem(t, err, problem.TypeGamblingNotActivated)

	// Balances hide the frozen sandbox until they activate.
	_, game, _, err := h.svc.GetBalances(ctx, user)
	if err != nil {
		t.Fatalf("GetBalances: %v", err)
	}
	if game != nil {
		t.Fatal("a legacy sandbox holder must not appear activated")
	}

	// After consent + activation the SAME sandbox wallet comes back: activation must
	// not orphan an existing balance by minting a second sandbox wallet.
	acceptGambling(t, h, user)
	_, sandbox, err := h.svc.ActivateGambling(ctx, user, wallet.KYCVerified, "", "")
	if err != nil {
		t.Fatalf("ActivateGambling: %v", err)
	}
	if sandbox.WalletID != legacy.WalletID {
		t.Fatalf("activation minted a NEW sandbox wallet (%s), orphaning the legacy one (%s)",
			sandbox.WalletID, legacy.WalletID)
	}
}
