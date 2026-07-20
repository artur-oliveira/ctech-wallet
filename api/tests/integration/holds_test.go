//go:build integration

package integration_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/wallet/api/internal/domain/id"
	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/problem"
)

// backdateHold simulates a hold that has sat `held` past the stale-hold
// ceiling, directly rewriting created_at — there is no API path to do this
// (a hold's age is a fact, not something any caller sets).
func backdateHold(t *testing.T, holdID, createdAt string) {
	t.Helper()
	_, err := db.UpdateItem(context.Background(), &dynamodb.UpdateItemInput{
		TableName:                 aws.String(table(wallet.TableHolds)),
		Key:                       map[string]dtypes.AttributeValue{"pk": &dtypes.AttributeValueMemberS{Value: holdID}},
		UpdateExpression:          aws.String("SET created_at = :c"),
		ExpressionAttributeValues: map[string]dtypes.AttributeValue{":c": &dtypes.AttributeValueMemberS{Value: createdAt}},
	})
	if err != nil {
		t.Fatalf("backdateHold: %v", err)
	}
}

// --- game wallet holds (skill-game integration, e.g. ctech-poker) ---

func TestHoldGameReservesAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := fundedAndActivated(t, h, 10000)
	if _, _, err := h.svc.FundGame(ctx, user, 8000, "idem-fund"); err != nil {
		t.Fatalf("FundGame: %v", err)
	}

	hold, err := h.svc.HoldGame(ctx, user, 5000, "table-1:seat-2", "idem-hold")
	if err != nil {
		t.Fatalf("HoldGame: %v", err)
	}
	if hold.Status != wallet.HoldHeld || hold.Amount != 5000 {
		t.Fatalf("unexpected hold: %+v", hold)
	}

	_, game, _, err := h.repo.LoadWallets(ctx, user)
	if err != nil {
		t.Fatalf("LoadWallets: %v", err)
	}
	if game.Balance != 3000 {
		t.Fatalf("game balance = %d, want 3000 (8000 funded - 5000 held)", game.Balance)
	}

	// Idempotent replay: same key must not debit twice.
	hold2, err := h.svc.HoldGame(ctx, user, 5000, "table-1:seat-2", "idem-hold")
	if err != nil {
		t.Fatalf("HoldGame replay: %v", err)
	}
	if hold2.HoldID != hold.HoldID {
		t.Fatalf("replay created a new hold: %s vs %s", hold2.HoldID, hold.HoldID)
	}
	if bal := balance(t, h, game.WalletID); bal != 3000 {
		t.Fatalf("replay moved money again: game = %d, want 3000", bal)
	}
}

func TestHoldGameCannotOverdrawGame(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := fundedAndActivated(t, h, 10000)
	if _, _, err := h.svc.FundGame(ctx, user, 3000, "idem-fund"); err != nil {
		t.Fatalf("FundGame: %v", err)
	}

	_, err := h.svc.HoldGame(ctx, user, 3001, "table-1", "idem-hold-over")
	wantProblem(t, err, problem.TypeInsufficientBalance)

	_, game, _, _ := h.repo.LoadWallets(ctx, user)
	if game.Balance != 3000 {
		t.Fatalf("failed hold must not move money: game = %d, want 3000", game.Balance)
	}
}

func TestHoldGameRequiresActivation(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()
	fund(t, h, user, 10000)

	_, err := h.svc.HoldGame(ctx, user, 1000, "table-1", "idem-hold")
	wantProblem(t, err, problem.TypeGamblingNotActivated)
}

func TestReleaseHoldRefundsInFullAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := fundedAndActivated(t, h, 10000)
	if _, _, err := h.svc.FundGame(ctx, user, 5000, "idem-fund"); err != nil {
		t.Fatalf("FundGame: %v", err)
	}
	hold, err := h.svc.HoldGame(ctx, user, 4000, "table-1", "idem-hold")
	if err != nil {
		t.Fatalf("HoldGame: %v", err)
	}

	released, err := h.svc.ReleaseHold(ctx, user, hold.HoldID, "idem-release")
	if err != nil {
		t.Fatalf("ReleaseHold: %v", err)
	}
	if released.Status != wallet.HoldReleased {
		t.Fatalf("status = %q, want released", released.Status)
	}
	if bal := balance(t, h, hold.WalletID); bal != 5000 {
		t.Fatalf("game balance = %d, want 5000 (full refund)", bal)
	}

	// A second release is a benign no-op — must not credit again.
	released2, err := h.svc.ReleaseHold(ctx, user, hold.HoldID, "idem-release-2")
	if err != nil {
		t.Fatalf("ReleaseHold retry: %v", err)
	}
	if released2.Status != wallet.HoldReleased {
		t.Fatalf("retry status = %q, want released", released2.Status)
	}
	if bal := balance(t, h, hold.WalletID); bal != 5000 {
		t.Fatalf("retry must not credit again: game balance = %d, want 5000", bal)
	}
}

// A hold can only transition once: two concurrent releases of the same hold
// must not both succeed in crediting — exactly one wins, the other observes
// the already-released state.
func TestConcurrentReleaseHoldOnlyCreditsOnce(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := fundedAndActivated(t, h, 10000)
	if _, _, err := h.svc.FundGame(ctx, user, 5000, "idem-fund"); err != nil {
		t.Fatalf("FundGame: %v", err)
	}
	hold, err := h.svc.HoldGame(ctx, user, 4000, "table-1", "idem-hold")
	if err != nil {
		t.Fatalf("HoldGame: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, _ = h.svc.ReleaseHold(ctx, user, hold.HoldID, "idem-release")
		}(i)
	}
	wg.Wait()

	if bal := balance(t, h, hold.WalletID); bal != 5000 {
		t.Fatalf("concurrent releases credited more than once: game balance = %d, want 5000", bal)
	}
}

// THE REGRESSION: a cash-out larger than any single hold must succeed — a
// player's final stack is the table's redistribution of every seated
// player's buy-in, not bounded by their own reservation. If this ever fails,
// a winning player could never be credited their winnings through this route.
func TestCashoutGameNotBoundedByHoldAmount(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	userA := fundedAndActivated(t, h, 20000)
	if _, _, err := h.svc.FundGame(ctx, userA, 20000, "idem-fund-a"); err != nil {
		t.Fatalf("FundGame A: %v", err)
	}

	// Two of A's own holds; the cash-out amount (20000) exceeds a single hold
	// (10000) — proving the amount is not bounded by any one hold's value. Both
	// holds are A's, never another user's (SEC-07).
	holdA1, err := h.svc.HoldGame(ctx, userA, 10000, "table-1", "idem-hold-a1")
	if err != nil {
		t.Fatalf("HoldGame A1: %v", err)
	}
	holdA2, err := h.svc.HoldGame(ctx, userA, 10000, "table-1", "idem-hold-a2")
	if err != nil {
		t.Fatalf("HoldGame A2: %v", err)
	}

	entryA, err := h.svc.CashoutGame(ctx, userA, 20000, "table-1", []string{holdA1.HoldID, holdA2.HoldID}, "idem-cashout-a")
	if err != nil {
		t.Fatalf("CashoutGame A: %v", err)
	}
	if entryA.Amount != 20000 {
		t.Fatalf("cashout amount = %d, want 20000 (more than a single hold)", entryA.Amount)
	}

	_, gameA, _, err := h.repo.LoadWallets(ctx, userA)
	if err != nil {
		t.Fatalf("LoadWallets A: %v", err)
	}
	if gameA.Balance != 20000 {
		t.Fatalf("A's game balance = %d, want 20000", gameA.Balance)
	}

	holdA1Row, err := h.repo.GetHold(ctx, holdA1.HoldID)
	if err != nil {
		t.Fatalf("GetHold A1: %v", err)
	}
	holdA2Row, err := h.repo.GetHold(ctx, holdA2.HoldID)
	if err != nil {
		t.Fatalf("GetHold A2: %v", err)
	}
	if holdA1Row.Status != wallet.HoldSettled || holdA2Row.Status != wallet.HoldSettled {
		t.Fatalf("holds not settled: A1=%q A2=%q", holdA1Row.Status, holdA2Row.Status)
	}
}

// TestCashoutGameRejectsAnotherUsersHold proves SEC-07: a cash-out listing a
// hold that belongs to a different user is rejected (Forbidden) and mutates
// nothing — neither the credit nor any hold transition happens.
func TestCashoutGameRejectsAnotherUsersHold(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	userA := fundedAndActivated(t, h, 20000)
	userB := fundedAndActivated(t, h, 10000)
	if _, _, err := h.svc.FundGame(ctx, userA, 20000, "idem-fund-a"); err != nil {
		t.Fatalf("FundGame A: %v", err)
	}
	if _, _, err := h.svc.FundGame(ctx, userB, 10000, "idem-fund-b"); err != nil {
		t.Fatalf("FundGame B: %v", err)
	}
	holdA, err := h.svc.HoldGame(ctx, userA, 10000, "table-1", "idem-hold-a")
	if err != nil {
		t.Fatalf("HoldGame A: %v", err)
	}
	holdB, err := h.svc.HoldGame(ctx, userB, 10000, "table-1", "idem-hold-b")
	if err != nil {
		t.Fatalf("HoldGame B: %v", err)
	}

	if _, err := h.svc.CashoutGame(ctx, userA, 5000, "table-1", []string{holdB.HoldID}, "idem-cashout-evil"); err == nil {
		t.Fatal("expected Forbidden for another user's hold, got nil")
	} else {
		wantProblem(t, err, problem.TypeForbidden)
	}
	// Neither hold may have been settled, and A's game balance must be unchanged.
	holdARow, _ := h.repo.GetHold(ctx, holdA.HoldID)
	holdBRow, _ := h.repo.GetHold(ctx, holdB.HoldID)
	if holdARow.Status != wallet.HoldHeld || holdBRow.Status != wallet.HoldHeld {
		t.Fatalf("holds must stay held, got A=%q B=%q", holdARow.Status, holdBRow.Status)
	}
	_, gameA, _, _ := h.repo.LoadWallets(ctx, userA)
	if gameA.Balance != 10000 {
		t.Fatalf("A's game balance = %d, want 10000 (no credit on rejected cash-out)", gameA.Balance)
	}
}

// A retry after a prior partial failure (some holds already settled by the
// earlier, since-crashed attempt) must not fail the whole cash-out.
func TestCashoutGameRetryAfterPartialFailureIsBenign(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := fundedAndActivated(t, h, 10000)
	if _, _, err := h.svc.FundGame(ctx, user, 5000, "idem-fund"); err != nil {
		t.Fatalf("FundGame: %v", err)
	}
	hold, err := h.svc.HoldGame(ctx, user, 5000, "table-1", "idem-hold")
	if err != nil {
		t.Fatalf("HoldGame: %v", err)
	}
	if _, err := h.repo.UpdateHoldStatus(ctx, hold.HoldID, wallet.HoldHeld, wallet.HoldSettled); err != nil {
		t.Fatalf("UpdateHoldStatus: %v", err)
	}

	if _, err := h.svc.CashoutGame(ctx, user, 5000, "table-1", []string{hold.HoldID}, "idem-cashout-retry"); err != nil {
		t.Fatalf("CashoutGame retry must not fail on an already-settled hold: %v", err)
	}
}

// --- stale-hold reconciliation (Invariant #12 analog) ---

// A hold stuck `held` past the ceiling is alarmed, never auto-resolved: the
// calling skill game's own crash-recovery may still resume the table and call
// release/cashout itself, and auto-resolving here would race that.
func TestSweepStaleHoldsAlarmsWithoutResolving(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := fundedAndActivated(t, h, 10000)
	if _, _, err := h.svc.FundGame(ctx, user, 5000, "idem-fund"); err != nil {
		t.Fatalf("FundGame: %v", err)
	}
	hold, err := h.svc.HoldGame(ctx, user, 4000, "table-1", "idem-hold")
	if err != nil {
		t.Fatalf("HoldGame: %v", err)
	}
	// Backdate the hold past the 24h ceiling, simulating a table that never resolved.
	backdated := time.Now().Add(-25 * time.Hour).UTC().Format(time.RFC3339Nano)
	backdateHold(t, hold.HoldID, backdated)

	alarmed, err := h.svc.SweepStaleHolds(ctx)
	if err != nil {
		t.Fatalf("SweepStaleHolds: %v", err)
	}
	if alarmed != 1 {
		t.Fatalf("alarmed = %d, want 1", alarmed)
	}

	// Never auto-resolved — still held, money still reserved.
	row, err := h.repo.GetHold(ctx, hold.HoldID)
	if err != nil {
		t.Fatalf("GetHold: %v", err)
	}
	if row.Status != wallet.HoldHeld {
		t.Fatalf("stale sweep must never resolve a hold, status = %q", row.Status)
	}
}

func TestSweepStaleHoldsIgnoresFreshHolds(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := fundedAndActivated(t, h, 10000)
	if _, _, err := h.svc.FundGame(ctx, user, 5000, "idem-fund"); err != nil {
		t.Fatalf("FundGame: %v", err)
	}
	if _, err := h.svc.HoldGame(ctx, user, 4000, "table-1", "idem-hold-fresh"); err != nil {
		t.Fatalf("HoldGame: %v", err)
	}

	alarmed, err := h.svc.SweepStaleHolds(ctx)
	if err != nil {
		t.Fatalf("SweepStaleHolds: %v", err)
	}
	if alarmed != 0 {
		t.Fatalf("alarmed = %d, want 0 for a fresh hold", alarmed)
	}
}
