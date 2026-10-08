# Statement API (balance before, typed entries) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `GET /v1.0/wallet/:type/ledger` returns typed entries that include `balance_before` next to `balance_after`, stops leaking raw DynamoDB attributes (`pk`, `sk`, `idempotency_key`), and has a pinned pagination contract the infinite-scroll UI can rely on.

**Architecture:** Pagination already exists (cursor + `limit`, newest first, max 200). The only server work is to stop marshalling raw DynamoDB items to JSON and instead decode them into `wallet.LedgerEntry`, wrapped in a tiny response view that derives `balance_before = balance_after - amount`. No schema change, no migration: `balance_after` is already stored on every entry. The two purchase lists (`/wallet/sandbox/purchases`, `/wallet/product-purchases`) are already cursor-paginated and are left untouched.

**Tech Stack:** Go, Fiber v3, DynamoDB (aws-sdk-go-v2, attributevalue).

**Spec:** `docs/specs/2026-10-08-wallet-restoration-design.md` (section 3).

## Global Constraints

- Amounts are integer centavos (sandbox: integer credits). `balance_before` is computed as `balance_after - amount` in `int64`; never floats, never derived by the client.
- The ledger stays append-only; this is a read-path change only. The authoritative balance is `wallets.balance`, never the ledger (Invariant #2). `balance_before` is display metadata.
- Response keys the UI already consumes must not change: `items`, `next_cursor`, `has_next`, and per-entry `entry_id`, `type`, `amount`, `balance_after`, `ref`, `description`, `created_at`. New keys: `balance_before`, and `wallet_id` (the UI type already declares it).
- Internal attributes must NOT appear in the response: `pk`, `sk`, `idempotency_key`.
- Errors are RFC 7807 via `sendProblem`.
- Conventional Commits, no emojis, NO attribution trailers. Work on a branch (`feat/statement-balance-before`).

## Review Focus

- A page boundary inside a run of entries with the same timestamp must not duplicate or skip an entry (cursor continuity across pages).
- A debit entry (negative `amount`) must give `balance_before > balance_after`; a credit the opposite; a zero-amount entry (should not exist) gives equal values.
- `limit=0`, `limit=-5`, `limit=abc` and `limit=100000` fall back to the default or the cap and never error.
- A tampered or garbage `cursor` must yield an empty first page or a 400, never a 500 and never another wallet's entries.
- The `sandbox` statement keeps working for a user who never activated gambling (read-only history), and `game` still returns `gambling-not-activated` until activation.

---

### Task 1: Typed ledger view with `balance_before`

**Files:**
- Create: `api/internal/api/v1/ledger_view.go`
- Modify: `api/internal/api/v1/helpers.go:140-155` (`sendStatement`)
- Test: `api/internal/api/v1/ledger_view_test.go`

**Interfaces:**
- Consumes: `repositories.QueryResult{Items []map[string]types.AttributeValue; LastEvaluatedKey}`, `repositories.DecodeItems[T]`, `wallet.LedgerEntry`.
- Produces:
  - `type ledgerEntryView struct { wallet.LedgerEntry; BalanceBefore int64 \`json:"balance_before"\` }`
  - `func newLedgerEntryView(e wallet.LedgerEntry) ledgerEntryView`
  - `sendStatement` now emits `[]ledgerEntryView`.

- [ ] **Step 1: Write the failing test**

`api/internal/api/v1/ledger_view_test.go`:

```go
package v1

import (
	"encoding/json"
	"testing"

	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
)

func TestLedgerEntryViewBalanceBefore(t *testing.T) {
	cases := []struct {
		name         string
		amount, after int64
		wantBefore   int64
	}{
		{"credit", 5000, 15000, 10000},
		{"debit", -3000, 7000, 10000},
		{"first credit on empty wallet", 1000, 1000, 0},
		{"debit to zero", -500, 0, 500},
	}
	for _, tc := range cases {
		v := newLedgerEntryView(wallet.LedgerEntry{Amount: tc.amount, BalanceAfter: tc.after})
		if v.BalanceBefore != tc.wantBefore {
			t.Errorf("%s: before = %d, want %d", tc.name, v.BalanceBefore, tc.wantBefore)
		}
	}
}

func TestLedgerEntryViewJSONShape(t *testing.T) {
	v := newLedgerEntryView(wallet.LedgerEntry{
		WalletID: "w1", SK: "sk-secret", EntryID: "e1", Type: "deposit", Amount: 100, BalanceAfter: 300,
		IdempotencyKey: "idem-secret", Ref: "tx1", Description: "Depósito PIX", CreatedAt: "2026-10-08T12:00:00Z",
	})
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"wallet_id", "entry_id", "type", "amount", "balance_after", "balance_before", "ref", "description", "created_at"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing key %q in %s", k, raw)
		}
	}
	for _, k := range []string{"pk", "sk", "idempotency_key"} {
		if _, ok := m[k]; ok {
			t.Errorf("internal key %q leaked: %s", k, raw)
		}
	}
	if m["balance_before"].(float64) != 200 {
		t.Errorf("balance_before = %v", m["balance_before"])
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd api && go test ./internal/api/v1/ -run LedgerEntryView -v`
Expected: FAIL (`undefined: newLedgerEntryView`).

- [ ] **Step 3: Implement the view**

`api/internal/api/v1/ledger_view.go`:

```go
package v1

import "gopkg.aoctech.app/wallet/api/internal/domain/wallet"

// ledgerEntryView is the statement row sent to clients: the immutable ledger
// entry plus the balance the wallet held before it. balance_before is derived,
// never stored, and is display metadata only: the authoritative balance lives in
// wallets.balance (Invariant #2). Embedding keeps the JSON keys of
// wallet.LedgerEntry, whose SK and IdempotencyKey are tagged `json:"-"`, so the
// internal attributes never reach the client.
type ledgerEntryView struct {
	wallet.LedgerEntry
	BalanceBefore int64 `json:"balance_before"`
}

func newLedgerEntryView(e wallet.LedgerEntry) ledgerEntryView {
	return ledgerEntryView{LedgerEntry: e, BalanceBefore: e.BalanceAfter - e.Amount}
}
```

- [ ] **Step 4: Switch `sendStatement` to the typed view**

Replace the body of `sendStatement` in `helpers.go`:

```go
func sendStatement(c fiber.Ctx, result *repositories.QueryResult) error {
	entries, err := repositories.DecodeItems[wallet.LedgerEntry](result.Items)
	if err != nil {
		return sendProblem(c, err)
	}
	items := make([]ledgerEntryView, 0, len(entries))
	for _, e := range entries {
		items = append(items, newLedgerEntryView(e))
	}
	return c.JSON(PaginatedResponse{
		Items:      items,
		NextCursor: buildNextCursor(result.LastEvaluatedKey),
		HasNext:    len(result.LastEvaluatedKey) > 0,
	})
}
```

Fix the imports in `helpers.go`: add `"gopkg.aoctech.app/wallet/api/internal/domain/wallet"`; drop `attributevalue` if `sendStatement` was its last user (`go build` will say; `rg "attributevalue" api/internal/api/v1/helpers.go`).

- [ ] **Step 5: Run to verify**

Run: `cd api && go build ./... && go test ./internal/api/v1/ -v 2>&1 | tail -30`
Expected: PASS. If an existing handler test asserted on a raw key such as `pk` (`rg '"pk"' api/internal/api/v1/*_test.go`), update it to `wallet_id`.

- [ ] **Step 6: Commit**

```bash
git checkout main && git pull origin main && git checkout -b feat/statement-balance-before
git add api && git commit -m "feat(api): add balance_before to statement entries and stop leaking internal attributes"
```

---

### Task 2: Pin the pagination contract with an integration test

**Files:**
- Test: `api/tests/integration/statement_test.go` (create)

**Interfaces:**
- Consumes: the harness in `api/tests/integration/setup_test.go` (read it first: it exposes the service, a way to create a user and fund `real`, and the Fiber app or handler needed to call the route). If the harness cannot call HTTP routes, call `svc.Statement(ctx, walletID, limit, startKey)` and `v1`'s `buildNextCursor`/`decodeCursor` helpers through the exported route instead; the assertions below hold either way.

- [ ] **Step 1: Write the tests**

```go
func TestStatementPagesCoverEveryEntryOnceNewestFirst(t *testing.T) {
	h := newHarness(t)
	u := h.newKYCUser("u-stmt")
	// Five movements: +1000, +500, -300, +200, -100  (balance 0 -> 1000 -> 1500 -> 1200 -> 1400 -> 1300)
	h.fundReal(u, 1000)
	h.fundReal(u, 500)
	h.debitReal(u, 300)
	h.fundReal(u, 200)
	h.debitReal(u, 100)

	var all []ledgerRow
	cursor := ""
	for page := 0; page < 10; page++ {
		rows, next, hasNext := h.getLedger(u, "real", 2, cursor) // limit=2 forces 3 pages
		all = append(all, rows...)
		if !hasNext {
			break
		}
		if next == "" {
			t.Fatal("has_next without next_cursor")
		}
		cursor = next
	}

	if len(all) != 5 {
		t.Fatalf("got %d entries across pages, want 5", len(all))
	}
	seen := map[string]bool{}
	for i, r := range all {
		if seen[r.EntryID] {
			t.Fatalf("entry %s returned twice", r.EntryID)
		}
		seen[r.EntryID] = true
		if r.BalanceBefore != r.BalanceAfter-r.Amount {
			t.Errorf("entry %d: before %d, after %d, amount %d", i, r.BalanceBefore, r.BalanceAfter, r.Amount)
		}
		// Newest first: this entry's balance_before equals the NEXT (older) entry's balance_after.
		if i+1 < len(all) && r.BalanceBefore != all[i+1].BalanceAfter {
			t.Errorf("chain broken between %d and %d: %d != %d", i, i+1, r.BalanceBefore, all[i+1].BalanceAfter)
		}
	}
	if all[0].BalanceAfter != 1300 || all[len(all)-1].BalanceBefore != 0 {
		t.Errorf("ends: newest after=%d oldest before=%d", all[0].BalanceAfter, all[len(all)-1].BalanceBefore)
	}
}

func TestStatementLimitBoundsAndGarbageCursor(t *testing.T) {
	h := newHarness(t)
	u := h.newKYCUser("u-stmt2")
	h.fundReal(u, 1000)

	for _, limit := range []string{"0", "-5", "abc", "100000"} {
		status, _ := h.getLedgerRaw(u, "real", "limit="+limit)
		if status != 200 {
			t.Errorf("limit=%s status %d, want 200", limit, status)
		}
	}
	status, body := h.getLedgerRaw(u, "real", "cursor=not-base64!!")
	if status >= 500 {
		t.Errorf("garbage cursor gave %d: %s", status, body)
	}
}

func TestStatementNeverLeaksAnotherWalletsEntries(t *testing.T) {
	h := newHarness(t)
	a, b := h.newKYCUser("u-a"), h.newKYCUser("u-b")
	h.fundReal(a, 1000)
	h.fundReal(b, 2000)
	rows, _, _ := h.getLedger(a, "real", 50, "")
	for _, r := range rows {
		if r.WalletID != h.realWalletID(a) {
			t.Fatalf("user A saw entry of wallet %s", r.WalletID)
		}
	}
}

func TestSandboxStatementReadableWithoutGamblingActivation(t *testing.T) {
	h := newHarness(t)
	u := h.newKYCUser("u-sbx")
	h.creditSandbox(u, 5000, "Prêmio de teste") // M2M lazily creates the sandbox wallet
	status, _ := h.getLedgerRaw(u, "sandbox", "")
	if status != 200 {
		t.Fatalf("sandbox statement status %d", status)
	}
	status, _ = h.getLedgerRaw(u, "game", "")
	if status == 200 {
		t.Fatal("game statement must be gambling-not-activated before activation")
	}
}
```

Define `type ledgerRow struct { WalletID, EntryID string; Amount, BalanceBefore, BalanceAfter int64 }` (JSON tags matching the response) in the test file. Implement `getLedger`/`getLedgerRaw`/`fundReal`/`debitReal`/`creditSandbox`/`realWalletID` as thin helpers in `setup_test.go` over what the harness already has (read it; reuse before adding). For `getLedger*`, build a request through the same Fiber app/route registration the harness uses for other HTTP-level tests; if none exists, register only `GET /v1.0/wallet/:type/ledger` with a test auth middleware that sets the user id and claims (see how `internal_test.go` in `api/internal/api/v1` fakes auth).

- [ ] **Step 2: Run to verify**

Run: `cd api && docker compose -f docker-compose.test.yml up -d && go test -tags integration ./tests/integration/ -run 'Statement' -v`
Expected: PASS. If the cursor test returns 500, that is a real bug in `decodeCursor`: fix it to ignore an undecodable cursor (treat as no cursor) or return `problem.BadRequest("cursor inválido")`, and keep this test as the regression.

- [ ] **Step 3: Commit**

```bash
git add api && git commit -m "test(api): pin statement pagination, balance chain and isolation"
```

---

### Task 3: Docs and cross-project note

**Files:**
- Modify: `api/ENDPOINTS.md` (ledger route), `docs/specs/2026-10-08-wallet-restoration-design.md` (section 3 status), `ui/src/lib/types/api.ts` is handled in the frontend plan.

- [ ] **Step 1:** In `ENDPOINTS.md`, document `GET /v1.0/wallet/{real|game|sandbox}/ledger?limit=&cursor=`: response `{items: [{entry_id, wallet_id, type, amount, balance_before, balance_after, ref?, description?, created_at}], next_cursor, has_next}`; `limit` default 50, max 200; cursor is opaque; newest first; 404/409 behavior (`gambling-not-activated` for `game` before activation). Note: "`pk`, `sk`, `idempotency_key` are no longer returned".
- [ ] **Step 2:** Cross-project: the only consumer is `ui` (`LedgerEntry` in `ui/src/lib/types/api.ts`; the frontend plan adds `balance_before`). `ctech-poker` does not read the ledger endpoint (`rg -n "/ledger" /home/artur-revgas/Documents/Projects/Ctech/ctech-poker/api` should return nothing; if it does, check it does not read `pk`).
- [ ] **Step 3: Verify and commit**

```bash
cd api && go build ./... && make test && make test-integration
git add -A && git commit -m "docs: document the statement response with balance_before"
```
