package services

import (
	"context"
	"testing"

	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/kycclient"
	"gopkg.aoctech.app/wallet/api/internal/problem"
	"gopkg.aoctech.app/wallet/api/internal/rail"
)

// mergedSvc is a service with GAME_WALLET_MERGED on and a user who accepted the
// gambling addendum, over the single-user stub.
func mergedSvc(repo *stubRepo) *WalletService {
	users := &stubUserRepo{user: &wallet.User{GamblingAddendumVersion: wallet.CurrentGamblingAddendumVersion}}
	svc := NewWalletService(repo, users, &stubAudit{}, &stubLocker{}, rail.NewFake(), &stubKYC{rec: &kycclient.KYC{}})
	svc.SetGameMerged(true)
	return svc
}

func TestMergedHoldDebitsTheRealWallet(t *testing.T) {
	repo := newStubRepo()
	h, err := mergedSvc(repo).HoldGame(context.Background(), "u1", 5000, "table-1", "idem-1")
	if err != nil {
		t.Fatalf("HoldGame: %v", err)
	}
	if h.WalletID != "w-real" {
		t.Fatalf("hold wallet = %q, want the real wallet", h.WalletID)
	}
}

func TestMergedHoldNeedsConsentToTheGamblingAddendum(t *testing.T) {
	repo := newStubRepo()
	svc := mergedSvc(repo)
	svc.users = &stubUserRepo{user: &wallet.User{}}
	_, err := svc.HoldGame(context.Background(), "u1", 5000, "table-1", "idem-1")
	isProblem(t, err, problem.TypeGamblingNotActivated)
}

func TestMergedCashoutOfAHoldTakenBeforeTheMergeCreditsReal(t *testing.T) {
	repo := newStubRepo()
	repo.holds["hold#old"] = &wallet.Hold{HoldID: "hold#old", WalletID: "w-game", UserID: "u1", Amount: 1000, TableRef: "t1", Status: wallet.HoldHeld}
	entry, err := mergedSvc(repo).CashoutGame(context.Background(), "u1", 1000, "t1", []string{"hold#old"}, "idem-c", "")
	if err != nil || entry.WalletID != "w-real" {
		t.Fatalf("entry=%+v err=%v", entry, err)
	}
}

func TestMergedReleaseOfAHoldTakenBeforeTheMergeRefundsReal(t *testing.T) {
	repo := newStubRepo()
	repo.holds["hold#old"] = &wallet.Hold{HoldID: "hold#old", WalletID: "w-game", UserID: "u1", Amount: 1000, TableRef: "t1", Status: wallet.HoldHeld}
	if _, err := mergedSvc(repo).ReleaseHold(context.Background(), "u1", "hold#old", "idem-r"); err != nil {
		t.Fatal(err)
	}
	if len(repo.creditCalls) != 1 || repo.creditCalls[0].WalletID != "w-real" {
		t.Fatalf("credits = %+v", repo.creditCalls)
	}
}

func TestMergedBalancesForReportsRealAsTheGameBalance(t *testing.T) {
	repo := newStubRepo()
	repo.real.Balance, repo.game.Balance = 7000, 0
	b, err := mergedSvc(repo).BalancesFor(context.Background(), "u1")
	if err != nil || b.GameBalance != 7000 {
		t.Fatalf("balances=%+v err=%v", b, err)
	}
}

func TestMergedGetBalancesHidesTheRetiredGameWallet(t *testing.T) {
	repo := newStubRepo()
	repo.game.RetiredAt = "2026-10-15T00:00:00Z"
	_, game, _, err := mergedSvc(repo).GetBalances(context.Background(), "u1")
	if err != nil || game != nil {
		t.Fatalf("game=%+v err=%v", game, err)
	}
}

func TestMergedEligibilityDoesNotNeedAGameWallet(t *testing.T) {
	repo := newStubRepo()
	repo.notActivated, repo.sandboxCreated = true, true // no game wallet, sandbox exists
	st, err := mergedSvc(repo).GameEligibilityFor(context.Background(), "u1")
	if err != nil || !st.Activated {
		t.Fatalf("eligibility=%+v err=%v", st, err)
	}
}

func TestMergedRingFenceOperationsAreRetired(t *testing.T) {
	svc := mergedSvc(newStubRepo())
	ctx := context.Background()
	_, _, err := svc.FundGame(ctx, "u1", 100, "k")
	isProblem(t, err, problem.TypeRouteRetired)
	if p := problem.RouteRetired(); p.Status != 410 {
		t.Fatalf("status = %d, want 410 gone", p.Status)
	}
	_, _, err = svc.ReturnFromGame(ctx, "u1", 100, "k")
	isProblem(t, err, problem.TypeRouteRetired)
	_, _, err = svc.PurchaseSandbox(ctx, "u1", 100, "k")
	isProblem(t, err, problem.TypeRouteRetired)
}

func TestMergedActivationCreatesNoGameWallet(t *testing.T) {
	repo := newStubRepo()
	repo.notActivated = true
	svc := mergedSvc(repo)
	svc.users = &stubUserRepo{user: &wallet.User{
		GamblingAddendumVersion: wallet.CurrentGamblingAddendumVersion,
		GameLimits:              &wallet.GameLimits{Daily: 1000, Weekly: 2000, Monthly: 3000},
	}}
	game, sandbox, err := svc.ActivateGambling(context.Background(), "u1", "basic", "1.1.1.1", "UA", 1000, 2000, 3000)
	if err != nil || game != nil || sandbox == nil || !repo.sandboxCreated {
		t.Fatalf("game=%v sandbox=%v err=%v", game, sandbox, err)
	}
}
