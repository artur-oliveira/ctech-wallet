# Real-Money Hold/Capture for Skill-Game Integration (poker) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:
> executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add three new M2M-only routes against the `game` wallet — `hold`, `release`, `cashout` — so
`ctech-poker`'s real-money mode (blocked on this per its own `ARCHITECTURE.md` § 4 / `PLAN.md` Phase 5)
can reserve a player's buy-in at table-join and settle their final stack at table-leave, without either
losing money on a crash or being forced into a payment-processor-style "capture ≤ hold" shape that
doesn't fit poker's redistribution of chips between players' own reservations.

**Architecture:** New `Hold` domain type + `wallet_holds` DynamoDB table (mirrors `wallet_withdrawals`'
shape, own `gsi_status` index). Three new service methods on `WalletService` — `HoldGame`,
`ReleaseHold`, `CashoutGame` — built the same way `sandboxOp`/`DebitReal` already are: `requireActivated`
→ `lock.Acquire` on the `game` wallet → `repo.Debit`/`repo.Credit` with a namespaced idempotency key. Two
new M2M scopes (`internal:wallet:game-hold`, `internal:wallet:game-cashout`). A new stale-hold
reconciliation sweep, same shape as the existing withdrawal reconciliation job, raising an alarm (never
auto-resolving) for any hold stuck in `held` past 24h.

**Tech Stack:** Go (existing `api` module), Fiber v3, DynamoDB (`aws-sdk-go-v2`), Valkey (existing
`lock.Locker`) — no new dependency, no new module, no new infra pattern.

**Spec:** `docs/specs/2026-07-19-poker-game-holds-design.md` — read it in full before starting,
especially the "Why cash-out isn't bounded by the hold" section; this plan assumes that reasoning.

## Global Constraints

- All amounts are integer centavos — never floats (root `CLAUDE.md`).
- Every string key, scope, entry type, table/GSI name is a named constant in
  `api/internal/domain/wallet/model.go` — no magic strings (root `CLAUDE.md`).
- Every debit carries `ConditionExpression: balance >= :amount` — never read-then-write balance
  (Financial Safety Invariant #1). `HoldGame`'s debit is a normal conditional debit; insufficient `game`
  balance is a normal `409 insufficient-balance`, nothing bespoke.
- Ledger is append-only; `wallet_ledger_entries` is never updated or deleted (Invariant #2).
- Every mutation requires an idempotency key, namespaced per operation exactly like `sandboxOp` does
  (`entryType + "#" + idemKey`) so `hold`/`release`/`cashout` can never collide on a reused raw key
  (Invariant #3).
- One operation per wallet at a time via `lock.Acquire`/`AcquireOrdered` on the `game` wallet id
  (Invariant #4) — `HoldGame`/`ReleaseHold`/`CashoutGame` only ever touch one wallet (`game`), so
  `lock.Acquire` (not `AcquireOrdered`) is correct, same as `sandboxOp`.
- Real money reaches `game` only via `real → game` (Invariant #7) — this plan does not add or need any
  other inbound edge; holds/releases/cash-outs all move money that is already inside `game`.
- `game` balance is real money, always withdrawable via `real` (Invariant #9) — a `Hold` reduces the
  *available* game balance the instant it's created (it's a real conditional debit, not a soft
  reservation on top of the balance), so `GetBalances`/the ledger statement continue to reflect the true
  spendable amount with no separate "available vs. held" computation needed anywhere else in the
  codebase.
- No money left in limbo (Invariant #12 analog) — a hold stuck `held` past the ceiling raises an alarm,
  never auto-resolves.
- `ctech-poker`'s M2M client must be authorized for both new scopes before this ships end-to-end — a
  `ctech-cdk`/account-side client-credentials grant change, tracked as its own task below, not assumed.

---

## File Structure

```
api/internal/domain/wallet/model.go        # MODIFIED — Hold status/entry consts, TableHolds, GSIHoldStatus, Hold struct
api/internal/domain/wallet/model_test.go   # MODIFIED — const/struct sanity tests, matching existing style

api/internal/repositories/holds.go         # NEW — CreateHold, GetHold, UpdateHoldStatus, ScanStaleHolds (gsi_status)
api/internal/repositories/holds_test.go    # NEW

api/internal/services/wallet.go            # MODIFIED — HoldGame, ReleaseHold, CashoutGame (+ requireActivated reuse)
api/internal/services/wallet_test.go       # MODIFIED — new test cases

api/internal/services/reconcile.go         # MODIFIED — stale-hold sweep alongside existing withdrawal reconciliation
api/internal/services/reconcile_test.go    # MODIFIED

api/internal/middleware/scope.go           # MODIFIED — ScopeWalletGameHold, ScopeWalletGameCashout

api/internal/api/v1/internal.go            # MODIFIED — holdGame, releaseHold, cashoutGame handlers
api/internal/api/v1/internal_test.go       # MODIFIED — route-registration + scope tests, mirroring TestRealDebitRouteRegistered
api/internal/api/v1/router.go              # MODIFIED — three new routes under internal/wallet/game

cdk/lib/dynamodb-stack.ts                  # MODIFIED — wallet_holds table (on-demand, same cap as siblings, gsi_status)
cdk/lib/*.ts (wherever the poker M2M client's grant lives)  # MODIFIED — grant the two new scopes
```

---

## Tasks

- [x] **1. Domain model.** Add to `api/internal/domain/wallet/model.go`: `HoldHeld`/`HoldReleased`/
  `HoldSettled` status consts, `EntryGameHoldDebit`/`EntryGameHoldRelease`/`EntryGameCashoutCredit` entry
  types, `TableHolds = "wallet_holds"`, `GSIHoldStatus = "gsi_hold_status"`, and the `Hold` struct exactly
  as sketched in the design spec. Table-driven const/struct test asserting every field has a
  `dynamodbav` tag (matches the existing pattern for `Withdrawal`/`PixDeposit`).

- [x] **2. Repository layer.** New `api/internal/repositories/holds.go`:
    - `CreateHold(ctx, walletID, userID, amount, tableRef, idemKey) (*wallet.Hold, error)` — single
      `TransactWriteItems`: conditional debit on the `game` wallet (`ConditionExpression: balance >= :amount`,
      same helper `repo.Debit` already uses under the hood — reuse it, don't hand-roll a second debit path)
        + put the new `Hold` item + idempotency guard, same three-way transaction shape `repo.Transfer`
          already uses for `ringTransfer`.
    - `GetHold(ctx, holdID) (*wallet.Hold, error)`.
    - `UpdateHoldStatus(ctx, holdID, fromStatus, toStatus) error` — conditional update
      (`ConditionExpression: #status = :from`) so a hold can only transition once; a second `release` or
      `cashout` racing the first fails closed instead of double-crediting.
    - `ScanStaleHolds(ctx, olderThan time.Time) ([]*wallet.Hold, error)` — `gsi_hold_status` query for
      `status = held` filtered by `created_at`, same shape as the withdrawal reconciliation scan.
    - Tests: insufficient-balance conditional failure, idempotent replay returns the prior `Hold` unchanged,
      concurrent `UpdateHoldStatus` race (one wins, one gets a conflict).

- [x] **3. Service layer.** In `api/internal/services/wallet.go`:
    - `HoldGame(ctx, userID, amount, tableRef, idemKey string) (*wallet.Hold, error)` —
      `requireActivated` → `lock.Acquire(game.WalletID)` → `repo.CreateHold`. Same shape as `sandboxOp`.
    - `ReleaseHold(ctx, holdID, idemKey string) (*wallet.Hold, error)` — load hold, require `status == held`,
      `lock.Acquire` on the hold's wallet, `repo.Credit` for the full original `Amount` with entry type
      `game_hold_release`, then `UpdateHoldStatus(held → released)`.
    -
  `CashoutGame(ctx, userID string, amount int64, tableRef string, holdIDs []string, idemKey string) (*wallet.LedgerEntry, error)` —
  `requireActivated` → `lock.Acquire(game.WalletID)` → `repo.Credit` for `amount` (entry type
  `game_cashout_credit`, `ref` = joined `holdIDs` + `tableRef` for audit) → `UpdateHoldStatus(held → settled)`
  for every id in `holdIDs`. **Do not** validate `amount` against the sum of `holdIDs`' amounts — the
  design spec explains why that check is actively wrong for poker's redistribution model. If any
  `UpdateHoldStatus` call finds a hold not in `held` (already settled/released), treat it as a benign
  idempotent-replay case, not an error — a caller retry after a prior partial failure must not fail the
  whole cash-out.
    - Tests: happy path for each of the three; `HoldGame` respects the conditional-balance failure;
      `ReleaseHold`/`CashoutGame` reject an already-`released`/`settled` hold; cash-out amount independent
      of hold sum (explicit test asserting a cash-out larger than any single hold succeeds, proving the
      "not bounded" behavior is intentional and regression-tested, not an oversight).

- [x] **4. Scopes.** Add `ScopeWalletGameHold = "internal:wallet:game-hold"` and
  `ScopeWalletGameCashout = "internal:wallet:game-cashout"` to `api/internal/middleware/scope.go`,
  following the exact naming convention of `ScopeWalletRealDebit`/`ScopePixConfirmDeposit`.

- [x] **5. HTTP handlers + routing.** In `api/internal/api/v1/internal.go`: `holdGame`, `releaseHold`,
  `cashoutGame` handlers, same shape as `sandboxCredit`/`realDebit` (bind body → call service → `sendProblem`
  on error → `fiber.StatusCreated`). New request DTOs (`HoldRequest`, `ReleaseRequest`, `CashoutRequest`)
  wherever `MovementOpRequest` already lives. Wire all three under
  `internal.Group("/wallet/game")` in `router.go`, each behind its matching `RequireScope`. Route
  registration tests mirroring `TestRealDebitRouteRegistered`.

- [x] **6. Stale-hold reconciliation.** Extend `api/internal/services/reconcile.go`'s existing scheduled
  job (or add a sibling job if the existing one is withdrawal-specific enough that bolting this on would
  blur its name) to scan `ScanStaleHolds` past a 24h ceiling and raise the same class of operational alarm
  already used for `WithdrawRefundFail`/stuck `processing` withdrawals — no auto-release, no auto-cashout.

- [x] **7. Infra.** Add `wallet_holds` table to `cdk/lib/dynamodb-stack.ts`: `TableV2`, on-demand billing
  with the same `{maxReadRequestUnits: 1000, maxWriteRequestUnits: 1000}` cap as every sibling table
  (confirm with whoever owns capacity planning whether poker's expected concurrency needs a higher cap —
  flagged as an open question in the design spec), `gsi_hold_status` GSI. Grant `ctech-poker`'s M2M
  client the two new scopes wherever its `client_credentials` grant is currently defined (find via the
  existing sandbox-credit/debit grant — poker already has an M2M client for those).

- [ ] **8. Cross-repo confirmation.** Once merged and deployed to a non-prod environment, hand the exact
  request/response shapes back to whoever is building `ctech-poker`'s Phase 5 wallet-integration code so
  its `ARCHITECTURE.md` § 4 sketch can be updated from "proposal" to "confirmed contract" — this was the
  explicit open decision #3 tracked in `ctech-poker/PLAN.md`.
