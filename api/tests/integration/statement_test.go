//go:build integration

package integration_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/wallet/api/internal/domain/id"
	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/repositories"
)

// seedReal credits the user's real wallet directly (no PIX leg).
func seedReal(t *testing.T, h *harness, user string, amount int64) *wallet.Wallet {
	t.Helper()
	ctx := context.Background()
	real, err := h.repo.EnsureRealWallet(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.repo.Credit(ctx, repositories.Mutation{
		WalletID: real.WalletID, Amount: amount, EntryType: wallet.EntryDeposit,
		Ref: "seed", IdempotencyKey: "seed#" + id.New(), ReqHash: "seed",
	}); err != nil {
		t.Fatal(err)
	}
	return real
}

func debitReal(t *testing.T, h *harness, walletID string, amount int64) {
	t.Helper()
	if _, _, err := h.repo.Debit(context.Background(), repositories.Mutation{
		WalletID: walletID, Amount: amount, EntryType: wallet.EntryWithdraw,
		Ref: "seed", IdempotencyKey: "seed#" + id.New(), ReqHash: "seed",
	}); err != nil {
		t.Fatal(err)
	}
}

func decodeEntries(t *testing.T, res *repositories.QueryResult) []wallet.LedgerEntry {
	t.Helper()
	entries, err := repositories.DecodeItems[wallet.LedgerEntry](res.Items)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

// Five movements: +1000 +500 -300 +200 -100 (balance 0>1000>1500>1200>1400>1300)
// paged two at a time: every entry exactly once, newest first, and the balance
// chain (this entry's before == the next older entry's after) is unbroken.
func TestStatementPagesCoverEveryEntryOnceNewestFirst(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()
	real := seedReal(t, h, user, 1000)
	seedReal(t, h, user, 500)
	debitReal(t, h, real.WalletID, 300)
	seedReal(t, h, user, 200)
	debitReal(t, h, real.WalletID, 100)

	var all []wallet.LedgerEntry
	var startKey map[string]types.AttributeValue
	for page := 0; page < 10; page++ {
		res, err := h.svc.Statement(ctx, real.WalletID, 2, startKey)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, decodeEntries(t, res)...)
		if len(res.LastEvaluatedKey) == 0 {
			break
		}
		startKey = res.LastEvaluatedKey
	}
	if len(all) != 5 {
		t.Fatalf("entries across pages = %d, want 5", len(all))
	}
	seen := map[string]bool{}
	for i, e := range all {
		if seen[e.EntryID] {
			t.Fatalf("entry %s returned twice", e.EntryID)
		}
		seen[e.EntryID] = true
		before := e.BalanceAfter - e.Amount
		if i+1 < len(all) && before != all[i+1].BalanceAfter {
			t.Errorf("chain broken between %d and %d: before=%d older.after=%d", i, i+1, before, all[i+1].BalanceAfter)
		}
	}
	if all[0].BalanceAfter != 1300 || all[len(all)-1].BalanceAfter-all[len(all)-1].Amount != 0 {
		t.Errorf("ends: newest after=%d", all[0].BalanceAfter)
	}
}

// A start key belonging to ANOTHER wallet must never surface that wallet's
// entries: DynamoDB rejects a start key outside the queried partition.
func TestStatementForeignStartKeyNeverLeaksAnotherWallet(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	a, b := "u-"+id.New(), "u-"+id.New()
	walletA := seedReal(t, h, a, 1000)
	walletB := seedReal(t, h, b, 2000)
	seedReal(t, h, b, 500)

	resB, err := h.svc.Statement(ctx, walletB.WalletID, 1, nil)
	if err != nil || len(resB.LastEvaluatedKey) == 0 {
		t.Fatalf("setup: %v %v", err, resB)
	}
	res, err := h.svc.Statement(ctx, walletA.WalletID, 50, resB.LastEvaluatedKey)
	t.Logf("foreign start key outcome: err=%v", err)
	if err == nil {
		for _, e := range decodeEntries(t, res) {
			if e.WalletID != walletA.WalletID {
				t.Fatalf("saw an entry of wallet %s", e.WalletID)
			}
		}
	}
}
