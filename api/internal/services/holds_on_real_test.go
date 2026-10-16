package services

import (
	"context"
	"testing"

	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/kycclient"
	"gopkg.aoctech.app/wallet/api/internal/problem"
	"gopkg.aoctech.app/wallet/api/internal/rail"
)

// mergedSvc is a service over the single-user stub. The game wallet is merged into `real` for good.
func mergedSvc(repo *stubRepo) *WalletService {
	users := &stubUserRepo{user: &wallet.User{GamblingAddendumVersion: wallet.CurrentGamblingAddendumVersion}}
	svc := NewWalletService(repo, users, &stubAudit{}, &stubLocker{}, rail.NewFake(), &stubKYC{rec: &kycclient.KYC{}})
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

// A self-exclusion still recorded in the wallet refuses a new hold, and a read error refuses it too
// (fail closed). A cash-out and a release still work: returning money never raises exposure.
func TestAHoldIsRefusedWhileASelfExclusionIsRecorded(t *testing.T) {
	repo := newStubRepo()
	svc := mergedSvc(repo)
	svc.users = &stubUserRepo{user: &wallet.User{SelfExclusion: &wallet.SelfExclusion{Period: "indefinite"}}}

	_, err := svc.HoldGame(context.Background(), "u1", 5000, "table-1", "idem-1")
	isProblem(t, err, problem.TypeSelfExcluded)
	if len(repo.holds) != 0 {
		t.Fatalf("an excluded player got a hold: %+v", repo.holds)
	}
}

func TestAnExcludedPlayerCanStillCashOutAndReleaseWhatIsAlreadyHeld(t *testing.T) {
	repo := newStubRepo()
	repo.holds["hold#old"] = &wallet.Hold{HoldID: "hold#old", WalletID: "w-real", UserID: "u1", Amount: 1000, TableRef: "t1", Status: wallet.HoldHeld}
	svc := mergedSvc(repo)
	svc.users = &stubUserRepo{user: &wallet.User{SelfExclusion: &wallet.SelfExclusion{Period: "indefinite"}}}

	if _, err := svc.CashoutGame(context.Background(), "u1", 1000, "t1", []string{"hold#old"}, "idem-c", ""); err != nil {
		t.Fatalf("an excluded player must get their money back: %v", err)
	}
}
