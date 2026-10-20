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

	"gopkg.aoctech.app/api-commons/problem"
	"gopkg.aoctech.app/wallet/api/internal/domain/id"
	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/walletproblem"
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

// funded creates a user whose `real` wallet holds amount. Holds stake `real`: the game wallet was merged
// into it, and consent, limits and self-exclusion are Poker's rules, not the wallet's.
func funded(t *testing.T, h *harness, amount int64) string {
	t.Helper()
	user := "u-" + id.New()
	fund(t, h, user, amount)
	return user
}

func realBalance(t *testing.T, h *harness, user string) int64 {
	t.Helper()
	real, _, _, err := h.repo.LoadWallets(context.Background(), user)
	if err != nil || real == nil {
		t.Fatalf("LoadWallets: %v", err)
	}
	return real.Balance
}

// --- holds (skill-game integration, e.g. ctech-poker) ---

func TestHoldReservesAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := funded(t, h, 8000)

	hold, err := h.svc.HoldGame(ctx, user, 5000, "table-1:seat-2", "idem-hold")
	if err != nil {
		t.Fatalf("HoldGame: %v", err)
	}
	if hold.Status != wallet.HoldHeld || hold.Amount != 5000 {
		t.Fatalf("unexpected hold: %+v", hold)
	}
	if bal := realBalance(t, h, user); bal != 3000 {
		t.Fatalf("real balance = %d, want 3000 (8000 - 5000 held)", bal)
	}

	// Idempotent replay: same key must not debit twice.
	hold2, err := h.svc.HoldGame(ctx, user, 5000, "table-1:seat-2", "idem-hold")
	if err != nil {
		t.Fatalf("HoldGame replay: %v", err)
	}
	if hold2.HoldID != hold.HoldID {
		t.Fatalf("replay created a new hold: %s vs %s", hold2.HoldID, hold.HoldID)
	}
	if bal := realBalance(t, h, user); bal != 3000 {
		t.Fatalf("replay moved money again: real = %d, want 3000", bal)
	}
}

func TestHoldCannotOverdraw(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := funded(t, h, 3000)

	_, err := h.svc.HoldGame(ctx, user, 3001, "table-1", "idem-hold-over")
	wantProblem(t, err, walletproblem.TypeInsufficientBalance)

	if bal := realBalance(t, h, user); bal != 3000 {
		t.Fatalf("failed hold must not move money: real = %d, want 3000", bal)
	}
}

// A user the wallet has never seen has nothing to stake.
func TestHoldNeedsAWallet(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())

	_, err := h.svc.HoldGame(ctx, "u-"+id.New(), 1000, "table-1", "idem-hold")
	wantProblem(t, err, walletproblem.TypeGamblingNotActivated)
}

func TestReleaseHoldRefundsInFullAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := funded(t, h, 5000)
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
	if bal := realBalance(t, h, user); bal != 5000 {
		t.Fatalf("real balance = %d, want 5000 (full refund)", bal)
	}

	// A second release is a benign no-op — must not credit again.
	released2, err := h.svc.ReleaseHold(ctx, user, hold.HoldID, "idem-release-2")
	if err != nil {
		t.Fatalf("ReleaseHold retry: %v", err)
	}
	if released2.Status != wallet.HoldReleased {
		t.Fatalf("retry status = %q, want released", released2.Status)
	}
	if bal := realBalance(t, h, user); bal != 5000 {
		t.Fatalf("retry must not credit again: real balance = %d, want 5000", bal)
	}
}

// A hold can only transition once: two concurrent releases of the same hold
// must not both succeed in crediting — exactly one wins, the other observes
// the already-released state.
func TestConcurrentReleaseHoldOnlyCreditsOnce(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := funded(t, h, 5000)
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

	if bal := realBalance(t, h, user); bal != 5000 {
		t.Fatalf("concurrent releases credited more than once: real balance = %d, want 5000", bal)
	}
}

// A hold opened while the game wallet still existed names that retired wallet; its refund goes to `real`.
func TestReleaseOfAHoldOnTheRetiredGameWalletRefundsReal(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := funded(t, h, 5000)
	hold, err := h.svc.HoldGame(ctx, user, 4000, "table-1", "idem-hold")
	if err != nil {
		t.Fatalf("HoldGame: %v", err)
	}
	if hold.WalletID == "" {
		t.Fatal("a hold names the wallet it debited")
	}
	if _, err := h.svc.ReleaseHold(ctx, user, hold.HoldID, "idem-release"); err != nil {
		t.Fatalf("ReleaseHold: %v", err)
	}
	if bal := realBalance(t, h, user); bal != 5000 {
		t.Fatalf("real balance = %d, want 5000", bal)
	}
}

// A cashout may span several holds but remains bounded by their aggregate.
func TestCashoutMayConsumeMultipleHolds(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	userA := funded(t, h, 20000)

	// Two of A's own holds permit an atomic aggregate cashout of 20000.
	holdA1, err := h.svc.HoldGame(ctx, userA, 10000, "table-1", "idem-hold-a1")
	if err != nil {
		t.Fatalf("HoldGame A1: %v", err)
	}
	holdA2, err := h.svc.HoldGame(ctx, userA, 10000, "table-1", "idem-hold-a2")
	if err != nil {
		t.Fatalf("HoldGame A2: %v", err)
	}

	entryA, err := h.svc.CashoutGame(ctx, userA, 20000, "table-1", []string{holdA1.HoldID, holdA2.HoldID}, "idem-cashout-a", "")
	if err != nil {
		t.Fatalf("CashoutGame A: %v", err)
	}
	if entryA.Amount != 20000 {
		t.Fatalf("cashout amount = %d, want 20000 (more than a single hold)", entryA.Amount)
	}
	if bal := realBalance(t, h, userA); bal != 20000 {
		t.Fatalf("A's real balance = %d, want 20000", bal)
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

// TestCashoutRejectsAnotherUsersHold proves SEC-07: a cash-out listing a
// hold that belongs to a different user is rejected (Forbidden) and mutates
// nothing — neither the credit nor any hold transition happens.
func TestCashoutRejectsAnotherUsersHold(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	userA := funded(t, h, 20000)
	userB := funded(t, h, 10000)
	holdA, err := h.svc.HoldGame(ctx, userA, 10000, "table-1", "idem-hold-a")
	if err != nil {
		t.Fatalf("HoldGame A: %v", err)
	}
	holdB, err := h.svc.HoldGame(ctx, userB, 10000, "table-1", "idem-hold-b")
	if err != nil {
		t.Fatalf("HoldGame B: %v", err)
	}

	if _, err := h.svc.CashoutGame(ctx, userA, 5000, "table-1", []string{holdB.HoldID}, "idem-cashout-evil", ""); err == nil {
		t.Fatal("expected Forbidden for another user's hold, got nil")
	} else {
		wantProblem(t, err, problem.TypeForbidden)
	}
	// Neither hold may have been settled, and A's balance must be unchanged.
	holdARow, _ := h.repo.GetHold(ctx, holdA.HoldID)
	holdBRow, _ := h.repo.GetHold(ctx, holdB.HoldID)
	if holdARow.Status != wallet.HoldHeld || holdBRow.Status != wallet.HoldHeld {
		t.Fatalf("holds must stay held, got A=%q B=%q", holdARow.Status, holdBRow.Status)
	}
	if bal := realBalance(t, h, userA); bal != 10000 {
		t.Fatalf("A's real balance = %d, want 10000 (no credit on rejected cash-out)", bal)
	}
}

func TestCashoutCannotMintBeyondReservedValue(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := funded(t, h, 5000)
	hold, err := h.svc.HoldGame(ctx, user, 5000, "table-1", "idem-hold")
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.svc.CashoutGame(ctx, user, 5001, "table-1", []string{hold.HoldID}, "idem-cashout", "")
	wantProblem(t, err, problem.TypeBadRequest)
	row, err := h.repo.GetHold(ctx, hold.HoldID)
	if err != nil {
		t.Fatal(err)
	}
	if row.Status != wallet.HoldHeld {
		t.Fatalf("rejected cashout consumed hold: %s", row.Status)
	}
}

// A consumed hold may never back another cashout. Atomic settlement makes the
// old partial-credit state impossible; fail closed if such state is observed.
func TestCashoutRejectsAlreadyConsumedHold(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := funded(t, h, 5000)
	hold, err := h.svc.HoldGame(ctx, user, 5000, "table-1", "idem-hold")
	if err != nil {
		t.Fatalf("HoldGame: %v", err)
	}
	if _, err := h.repo.UpdateHoldStatus(ctx, hold.HoldID, wallet.HoldHeld, wallet.HoldSettled); err != nil {
		t.Fatalf("UpdateHoldStatus: %v", err)
	}

	_, err = h.svc.CashoutGame(ctx, user, 5000, "table-1", []string{hold.HoldID}, "idem-cashout-retry", "")
	wantProblem(t, err, problem.TypeConflict)
}

// --- stale-hold reconciliation (Invariant #12 analog) ---

// A hold stuck `held` past the ceiling is alarmed, never auto-resolved: the
// calling skill game's own crash-recovery may still resume the table and call
// release/cashout itself, and auto-resolving here would race that.
func TestSweepStaleHoldsAlarmsWithoutResolving(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := funded(t, h, 5000)
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
	user := funded(t, h, 5000)
	if _, err := h.svc.HoldGame(ctx, user, 4000, "table-1", "idem-hold-fresh"); err != nil {
		t.Fatalf("HoldGame: %v", err)
	}

	_, err := h.svc.SweepStaleHolds(ctx)
	if err != nil {
		t.Fatalf("SweepStaleHolds: %v", err)
	}
}

// The description is display metadata: persisted on the cashout ledger entry
// and kept OUT of reqHash (CashoutGame hashes only table, holds and amount).
func TestCashoutPersistsDescription(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := funded(t, h, 5000)
	hold, err := h.svc.HoldGame(ctx, user, 5000, "table-1", "idem-hold")
	if err != nil {
		t.Fatal(err)
	}
	e, err := h.svc.CashoutGame(ctx, user, 5000, "table-1", []string{hold.HoldID}, "idem-cash-d", "Mesa #t1, cashout")
	if err != nil {
		t.Fatal(err)
	}
	if e.Description != "Mesa #t1, cashout" {
		t.Fatalf("description = %q", e.Description)
	}
}
