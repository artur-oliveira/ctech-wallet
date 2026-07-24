# Wallet API Security & Correctness Audit — `api/`

**Date:** 2026-07-19
**Scope:** Every Go file under `api/` (cmd, internal, tests excluded from findings except where they reveal gaps).
**Threat model:** Real-money custodial wallet, deployed as a distributed autoscaling service (multiple instances, shared
Valkey, each instance with its own in-process state). Findings are graded by money-safety impact.

## TL;DR (must-fix before next release)

1. **HIGH — `Withdraw` orphans the debit / panics the handler.** The balance debit and the withdrawal record are two
   separate calls; a transient `PutWithdrawal` failure (or a crash) between them leaves a committed debit with no
   `processing` row, so reconciliation never resolves it (Invariant #12 violated) and the handler nil-panics on retry.
2. **HIGH — Deposit record TTL (5 min) is shorter than realistic PIX charge validity and shorter than the reconcile
   sweep cadence.** A payment that lands after the local record is TTL-deleted is never credited and never swept (money
   in limbo at Inter, not in the wallet).
3. **MEDIUM — The reconcile sweep refunds a legitimately-paid deposit when the webhook CPF was never persisted** (the
   exact "webhook never arrived" case it exists to recover).
4. **MEDIUM — `CheckDeposit` uses `d+amount > limit`, an int64 overflow trap** that can let a deposit exceed the
   personal limit past the conditional counter bump.
5. **MEDIUM — M2M idempotency keys are not namespaced by user.** A reused/colliding key across users replays or
   conflicts across users.
6. **MEDIUM — Cross-replica Inter-token invalidation gap.** `Invalidate()` clears only the local + shared cache; other
   replicas keep the revoked bearer in their in-process hot cache until it expires.

---

## Findings

### SEC-01 · HIGH · `Withdraw` can orphan a debit and panic the handler

**Files:** `internal/services/wallet.go:502-588` (esp. `Withdraw`), `internal/repositories/wallet.go:283-321` (
`DebitWithFee`), `internal/api/v1/wallet.go:53-72` (`createWithdrawal`).

`Withdraw` does three steps that are *not* atomic together:

1. `DebitWithFee(...)` commits the balance debit + ledger + idempotency guard (one `TransactWriteItems`).
2. `PutWithdrawal(w)` is a **separate** `TransactWriteItems`.
3. `pix.Transfer(...)` (separate).

If step 1 succeeds and step 2 fails (transient DynamoDB throttle/timeout, or a process crash), the money is debited from
`real` with **no** `withdrawals` row. `ReconcileWithdrawals` only scans the `withdrawals` table for `processing` rows,
so the orphaned debit is never completed or reversed → money in limbo (Invariant #12).

The retry path makes it worse. On any later retry the same `Idempotency-Key` hits `DebitWithFee`'s guard (
`resolveTxErr` → replay) and `Withdraw` returns `GetWithdrawal(withdrawalID)` — which is `nil` because the record was
never written. `createWithdrawal` then dereferences `w.Status` → **nil-pointer panic** (HTTP 500 with stack).

**Fix (Option A — atomic, preferred):** co-write the withdrawal record inside `DebitWithFee`'s transaction (cross-table
`TransactWriteItems` is allowed in the same account/region). Add the `withdrawal` `BuildPutTxItemIfAbsent` item as an
`extra` to `DebitWithFee`, keyed by `withdrawalID` with `status=processing`. Then debit + record commit atomically; the
`pix.Transfer` call stays separate (its failure is already handled by reconciliation). On replay, the guard *and* the
record both exist, so `GetWithdrawal` returns the row.
**Fix (defensive):** in `createWithdrawal`, guard `if w == nil { return problem.InternalServer(...) }` before
`w.Status`.

**Test:** integration — `DebitWithFee` succeeds, `PutWithdrawal` forced to fail → assert a `processing` row exists (or
the debit is rolled back); retry with same key returns the row, no panic.

---

### SEC-02 · HIGH · Deposit record TTL (5 min) vs PIX charge validity & sweep cadence

**Files:** `internal/services/wallet.go:28` (`depositTTLMinutes = 5`), `:18` (`sweepAgeThreshold = 3m`),
`internal/repositories/wallet.go:395-417`, `internal/api/v1/internal.go:12-21` (`confirmDeposit`),
`cmd/reconcile/main.go:95-99`.

`PixDeposit.TTL` is 5 minutes. `SweepPendingDeposits` only re-queries deposits older than 3 min *that are
still `pending`* (rows not yet TTL-deleted). Reconciliation runs as a scheduled Lambda (`cmd/reconcile`, EventBridge).
Consequences:

- If the reconcile Lambda's interval exceeds ~2 min (3 min sweep threshold → 5 min hard delete), a pending deposit can
  be TTL-deleted *before* any sweep sees it.
- If the payer pays a QR code more than 5 min after creation (PIX charges at Inter are commonly valid far longer than 5
  min), `ConfirmDeposit`'s `GetDeposit` returns `nil` → silent no-op → **the user's money is taken by Inter but never
  credited and never refunded**.

The design intent ("the sweep re-queries Inter once before [TTL]") is defeated by a TTL shorter than both Inter's charge
validity and the sweep interval.

**Fix:** Set `depositTTLMinutes` to match Inter's actual charge expiry (e.g. 30–60 min). Ensure `SweepPendingDeposits`
covers *all* `pending` deposits up to that TTL (it already queries by `status=pending`; just make the TTL the real
ceiling). Document the required EventBridge cadence (must be << TTL, e.g. every 1–2 min) as an operational invariant.
Consider not relying on DynamoDB TTL for in-flight deposits at all — drive expiry from an explicit `expires_at`/status
transition instead, so a missed sweep is visible rather than silent.

**Test:** integration — create a pending deposit, advance clock past `sweepAgeThreshold` but before new TTL, run sweep,
assert it is credited on re-query.

---

### SEC-03 · MEDIUM · Sweep refunds a paid deposit when payer CPF was never persisted

**Files:** `internal/services/wallet.go:241-331` (`ConfirmDeposit`), `:293` (CPF gate), `:97-112` (
`SweepPendingDeposits` calls `ConfirmDeposit(txid, "", "")`).

`ConfirmDeposit` treats an empty `dep.PayerCPF` as a CPF mismatch (
`if dep.PayerCPF == "" || !maskedCPFMatches(...) → rejectMismatch → Refund`). The sweep calls it with empty CPF/Name. If
the Inter webhook *never arrived at all* (the precise case the sweep exists to recover), `dep.PayerCPF` was never
persisted, so the sweep **refunds a genuinely paid deposit** instead of crediting it. The recovery safety-net fails
exactly when it is needed.

The charge re-query (per design) does not return the payer CPF, so on the sweep path there is nothing to match against.

**Fix:** Distinguish "webhook CPF available" from "sweep, no CPF". When `dep.PayerCPF == ""` and we are on the
sweep/re-query-only path (or generally when no CPF was ever captured), skip the CPF-match gate (the re-query already
proves payment to *our* charge) and credit. Keep the strict CPF gate for the webhook-driven path where a CPF *is*
present. Add a `sweep bool` (or "no CPF captured") parameter to `ConfirmDeposit`. Note the residual anti-fraud trade-off
in the spec.

**Test:** integration — deposit paid, webhook never delivered (no payer CPF persisted), run sweep → assert credited (not
refunded).

---

### SEC-04 · MEDIUM · `CheckDeposit` int64 overflow bypasses the personal limit

**Files:** `internal/domain/wallet/responsible.go:100-113` (`CheckDeposit`), used by `internal/services/wallet.go:667`.

`case d+amount > limits.Daily` etc. `d` is the running window sum (`int64`). If `d + amount` wraps past `math.MaxInt64`
it becomes negative and the comparison is false → the breach is missed. The conditional `BumpDepositCounters` only fails
when the *persisted* counter differs from the read `prev`; it does **not** re-check the limit, so the overflowed `next`
is written and the money moves beyond the cap.

Realistically `d` reaches int64-max only with ~R$92B in one window, but the math is wrong and the invariant ("limits
enforced") should not depend on amounts staying small.

**Fix:** Replace `d+amount > limit` with the overflow-safe `amount > limit - d` (all three windows). Add a unit test for
the boundary/overflow case.

---

### SEC-05 · MEDIUM · M2M idempotency keys are not namespaced by user

**Files:** `internal/services/wallet.go:731-760` (`sandboxOp`), `:765-788` (`DebitReal`), `:797-818` (`HoldGame`);
`internal/api/v1/internal.go:24-62`.

`sandboxOp` builds the guard as `entryType + "#" + idemKey`, `DebitReal` as `EntryBillingDebit + "#" + idemKey`,
`HoldGame` as `EntryGameHoldDebit + "#" + idemKey` — **none include `userID`**. The `idemKey` comes from the M2M request
body (`body.IdempotencyKey`). Two M2M callers (or one caller across two users) that reuse the same `idemKey` collide:

- Different entry type → different guard key → no collision (so `credit` vs `debit` with same key is fine).
- Same entry type, different user, identical `(reason, amount)` → `reqHash` matches → `checkReplay` returns the *other
  user's* prior entry → money moves against the first user that claimed the key.
- Same entry type, different payload → `IdempotencyConflict`.

This is a cross-user idempotency hazard on trusted-but-shared internal paths.

**Fix:** Namespace every M2M idempotency key by the target user: `entryType + "#" + userID + "#" + idemKey`. Do it once
in the service layer (not per route) so it can't be forgotten.

**Test:** integration — `DebitReal` for user A with key `k`, then `DebitReal` for user B with key `k` → assert no
cross-user replay/credit; assert distinct guards.

---

### SEC-06 · MEDIUM · Cross-replica Inter-token invalidation gap

**Files:** `internal/pix/intertoken.go:97-105` (`Invalidate`), `:262-335` (local hot cache + `sharedValid`),
`internal/pix/lambda_client.go:86-92` (401 → `Invalidate` + force refresh).

`Invalidate()` clears `m.token`/`m.expiry` (this process) and `cache.Delete(tokenCacheKey)` (shared Valkey), but **other
replicas' in-process `m.token` hot cache is untouched**. After a 401, only the replica that got the 401 refreshes; every
other replica keeps sending the revoked bearer from its local hot cache until that cache expires (up to the token's full
lifetime, ~1h). Each eventually self-heals via its own 401, but there is a window of repeated rejected calls across the
fleet — a distributed-cache coherence gap the brief explicitly calls out.

**Fix options:**

- Treat the shared Valkey token as the *sole* authority and keep the in-process cache very short-lived (or purely a
  decode cache), so invalidation is observed fleet-wide within the floor window.
- Or broadcast invalidation (e.g. a Valkey pub/sub "token revoked" message) so all replicas drop their hot cache.

Note: `RefreshLoop` and `sharedValid` already make the shared cache the steady-state source, so the simplest safe change
is to stop trusting the local hot cache beyond a few seconds and always re-check `sharedValid` after `localValid`
misses.

---

### SEC-07 · MEDIUM · `releaseHold` / `cashoutGame` don't verify hold ownership

**Files:** `internal/services/wallet.go:825-919` (`ReleaseHold`, `CashoutGame`); `internal/api/v1/internal.go:80-105`.

`ReleaseHold(ctx, holdID, idemKey)` fetches the hold by `holdID` and releases it without checking it belongs to the
`body.UserID` the M2M caller named (the route doesn't even pass `userID`).
`CashoutGame(ctx, userID, amount, tableRef, holdIDs, idemKey)` credits `userID`'s game wallet and marks the supplied
`holdIDs` settled — but never verifies the hold IDs belong to `userID`. A compromised/buggy internal client (scope
`internal:wallet:game-hold` / `game-cashout`) can release or settle *another* user's holds, or credit one user while
settling another's holds.

Hold IDs are opaque/unguessable (`hold#<userID>#<idemKey>`), so this is an internal-trust-boundary issue, not a direct
external exploit — but it violates defense-in-depth for real money.

**Fix:** In `ReleaseHold`/`CashoutGame`, load each hold and assert `hold.UserID == userID` before mutating; return
`problem.Forbidden`/not-found otherwise.

---

### SEC-08 · MEDIUM · `InitiateDeposit` has no idempotency key → duplicate Inter charges on retry

    **Files:** `internal/api/v1/wallet.go:32-50` (`createDeposit`), `internal/services/wallet.go:190-227` (
    `InitiateDeposit`).

Unlike every other user mutation, `createDeposit` does **not** read `Idempotency-Key`. A client that retries a failed
`POST /wallet/deposits` (network blip, 5xx) opens a **second** Inter charge with a new `txid`. Each charge is
independent and each, if paid, credits the wallet once — so there is no direct money loss, but a user can end up with
multiple live QR codes for the same intent and may double-pay.

**Fix:** Require/accept `Idempotency-Key` on `createDeposit` and make `InitiateDeposit` idempotent (guard keyed by the
key; replay returns the existing `PixDeposit` + charge). Keep per-txid webhook idempotency as-is.

---

### SEC-09 · LOW · `reverse` (withdrawal reversal) does not acquire the wallet lock

**Files:** `internal/services/reconcile.go:69-90` (`reverse`), vs `internal/services/wallet.go:416-423` (
`reverseDeposit` *does* lock).

`reverse` credits `w.WalletID` (real) without `s.lock.Acquire`. `reverseDeposit` (deposit refund) acquires the lock. The
money is safe because DynamoDB increments are atomic, but the locking discipline is inconsistent and `BalanceAfter`
audit values can be computed against a stale read. Acquire the lock in `reverse` for consistency with every other
real-wallet mutation.

---

### SEC-10 · LOW · `SetGameLimits` is read-modify-write without a lock / conditional

**Files:** `internal/services/responsible.go:102-145`, `internal/repositories/user.go:63-68`.

`SetGameLimits` reads `u`, computes the applied limits (with pending-increase cooldown), then `UpsertAttrs` sets
`game_limits`. Two concurrent calls race (last-writer-wins on the whole field); a decrease could be lost. Not
money-moving, but the pending-increase logic should be conditional on the current value (or serialized) to avoid a lost
decrease.

---

### SEC-11 · LOW · Concurrent `FundGame` loser gets a misleading error

**Files:** `internal/services/wallet.go:667-683` (`FundGame`), `internal/repositories/wallet.go:612-630` (
`resolveTxErr`).

When two `FundGame` calls race, the loser's `BumpDepositCounters` condition (`#c = :prev`) fails, the whole transfer
transaction cancels, and `resolveTxErr` returns `InsufficientBalance` (because sign < 0). The limit actually held, but
the user sees "saldo insuficiente" and must retry. The money is safe; only the error is wrong.

**Fix:** When the transfer transaction fails *only* on the `extra` (counter) condition, return a `WalletBusy`/retryable
problem rather than `InsufficientBalance`.

---

### SEC-12 · LOW · Stale ledger `BalanceAfter` under concurrency

**Files:** `internal/repositories/wallet.go:255-279` (`mutate` reads `w.Balance` then builds `newEntry` with
`w.Balance+signed`), `:354-355`, `:62` (holds).

`BalanceAfter` is computed from a balance read *before* the atomic increment commits. A concurrent mutation between read
and write makes the stored `BalanceAfter` wrong. The authoritative balance is `Wallet.Balance` (atomic), so this is
audit-log inaccuracy only — but note it if any reconciliation logic ever trusts `BalanceAfter`.

---

## DRY / Documentation / Minor

- **DOC-01 · `repo.Transfer` comment is wrong.** `internal/repositories/wallet.go:323-332` says "Used by sandbox
  purchase (real → sandbox)". Invariant #7 forbids `real → sandbox`; the only callers (via `ringTransfer`) are
  `real→game`, `game→real`, `game→sandbox`. Fix the comment.
- **DRY-01 · Duplicated broadcast helpers.** `broadcastDepositConfirmed` and `broadcastWithdrawal` (
  `internal/services/wallet.go:445-480`) are ~identical; unify into one `broadcast(userID, eventType, payload)` helper.
- **IMPL-01 · Fee truncation bias.** `WithdrawalFee` (`internal/domain/wallet/fee.go:41`) uses `amount*bps/10000` (
  truncates). For odd centavo amounts this under-charges by up to 1 centavo. Within spec but a known downward bias;
  document or round.
- **IMPL-02 · `GetWalletsByUser` queries the user GSI** (`internal/repositories/wallet.go:91-105`); confirm markers are
  not projected into `gsi_user` (they would be returned as non-wallet items). The money path uses `loadByMarkers`, so
  this is only a risk if that method is wired to a route.

---

## Cross-project impact reviewed

- **api ↔ ctech-account:** KYC scope (`internal:account:kyc`), JWKS audience/issuer enforced in prod (
  `config.go:63-81`), step-up `max_age` honored by account. No code change needed in account for these findings; the CPF
  gate (SEC-03) is api-internal.
- **api ↔ pix-gateway (Lambda):** api never talks to Inter directly; every PIX op goes through `pix-gateway` with a
  shared OAuth bearer (SEC-06). The Lambda multiplexes 7 ops. No change needed in pix-gateway for these findings, but
  SEC-02/SEC-03 affect how api credits deposits confirmed by that Lambda.
- **api ↔ ui:** `DepositOutOfRange`/`AmountAboveLimit`/`StepUpRequired` carry bounds/max-age so the UI need not hardcode
  them (good). No UI change required by these findings.
- **cdn/infra:** SEC-02 requires an EventBridge cadence << deposit TTL; SEC-06 is aided by a shared Valkey (already
  required in prod). Confirm `GAMBLING_ENABLED` stays false until the limit engine ships (router.go:73) — SEC-04 is the
  limit engine's gate.

---

## Suggested Conventional Commit

```
docs: add wallet api security & correctness audit (2026-07-19)

Enumerate audit findings across api/: orphaned-withdraw-debit (HIGH),
deposit TTL vs sweep cadence (HIGH), sweep CPF-refund gap (MED),
CheckDeposit int64 overflow (MED), M2M idempotency not user-namespaced
(MED), cross-replica token invalidation (MED), hold ownership (MED),
missing deposit idempotency (MED). Includes fix sketches + tests.
```
