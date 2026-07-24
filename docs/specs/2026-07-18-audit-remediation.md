# Pre-Launch Audit Remediation

**Status:** proposed
**Date:** 2026-07-18
**Source:** `../../_analysis/GENERAL-REPORT.md`, `../../_analysis/ctech-wallet.md` (audit dated 2026-07-17)
**Depends on:** `docs/specs/2026-07-10-wallet-design.md`, `docs/specs/2026-07-12-three-wallet-topology-design.md`
**Blocks:** `ctech-billing` and `ctech-poker` real-money integration (both explicitly gated on F3 in the source audit)

---

## Purpose

Close every finding raised against `ctech-wallet` in the cross-project audit, in the priority order the audit
assigned (P0 → P1 → P2), plus the wallet-relevant cross-stack duplication items from `GENERAL-REPORT.md`. One
finding (F1) was already fixed same-day by commit `e20cf04` and is recorded here as closed, not re-specified.

Findings are numbered F1–F8 to match `ctech-wallet.md` exactly, so the audit and this spec stay cross-referenceable.

---

## Status snapshot (verified against current code, 2026-07-18)

| ID | Finding                                                                                | Audit severity | Current state                                                                                                   |
|----|----------------------------------------------------------------------------------------|----------------|-----------------------------------------------------------------------------------------------------------------|
| F1 | DynamoDB tables/GSIs capped at 5/10 RCU/WCU                                            | P0             | **Closed** — `cdk/lib/dynamodb-stack.ts:77-79,96-97` now cap at 1000 (commit `e20cf04`)                         |
| F2 | `Withdraw` idempotency check races the lock/write                                      | P0             | Open — confirmed at `api/internal/services/wallet.go:482-526`                                                   |
| F3 | No `internal:wallet:debit` route against `real`                                        | P0             | Open — `api/internal/api/v1/router.go:69-74` registers only sandbox credit/debit                                |
| F4 | Lock backend silently falls back to in-memory, no alarm                                | P1             | Open — `api/internal/lock/lock.go:36-42`                                                                        |
| F5 | CPF sourced from unauthenticated webhook field; `interWebhookSecret` unused            | P1             | Open — `cdk/lib/constants.ts:121` is the parameter's only reference in the repo                                 |
| F6 | No deposit-side reconciliation                                                         | P1             | Open — `api/internal/services/reconcile.go` only has `ReconcileWithdrawals`                                     |
| F7 | No aggregate/velocity deposit limits                                                   | P2             | Open                                                                                                            |
| F8 | No ledger contra-account for cash-in-transit                                           | P2             | Open                                                                                                            |
| —  | `rpc_types.go` / `pix-gateway/internal/rpc/types.go` hand-mirrored                     | cross-cutting  | Open — both still hand-synced, comment at `rpc_types.go:1-6` admits it                                          |
| —  | `tokenManager` duplicated (`kycclient` vs `pix-gateway/walletclient`)                  | cross-cutting  | Open — `walletclient.go:88-90` admits it; `pix-gateway` still has no `api-commons` dependency                   |
| —  | `repositories/base.go` reimplementing a shared pattern                                 | cross-cutting  | **Closed** — already delegates to `gopkg.aoctech.app/api-commons/dynamo.Base`; only the header comment is stale |
| —  | ALARM log lines not wired to a CloudWatch metric filter                                | observability  | Open                                                                                                            |
| —  | No concurrent-goroutine test proves lock+conditional-write actually stops double-spend | testing        | Open                                                                                                            |

---

## F2 — `Withdraw` idempotency race (P0)

### Problem

`api/internal/services/wallet.go:482-486` reads `GetWithdrawal` and returns early on a replay, **before** the
per-wallet lock is acquired at line 499. `PutWithdrawal` (`repositories/wallet.go:421-427`) is an unconditional
`PutItem`. Two concurrent calls with the same idempotency key can both pass the read, both call
`DebitWithFee` (which correctly rejects the second at the DynamoDB layer via its own transactional idempotency
guard — but the `replayed` return value is discarded at `wallet.go:510`), both unconditionally overwrite the
withdrawal record, and both call `s.pix.Transfer` for the same payout. Nothing but Inter's own (unverified)
dedup prevents a duplicate PIX transfer.

### Fix

1. Move the per-wallet lock acquisition (`s.lock.Acquire`) to **before** the `GetWithdrawal` replay check —
   the lock must guard the read-check-write sequence, not just the write.
2. Add `attribute_not_exists(pk)` to `PutWithdrawal`'s condition expression, matching every other
   idempotency-guarded write in this codebase. On condition failure, `GetWithdrawal` again and return the
   winner's record instead of erroring — this makes the fix correct even if two requests somehow race past the
   lock (different process, lock TTL expiry mid-request).
3. Stop discarding `DebitWithFee`'s `replayed` bool — if the debit was itself a replay, skip the second
   `PutWithdrawal`/`Transfer` entirely and return the prior withdrawal record.

### Acceptance criteria

- New integration test: N goroutines (`sync.WaitGroup`, real DynamoDB-local per `api/CLAUDE.md`'s Testing table)
  call `Withdraw` concurrently with the same idempotency key. Assert exactly one `PixClient.Transfer` invocation
  and exactly one ledger debit entry.
- Existing `TestWithdrawWalletBusy` / `TestWithdrawIdempotentReplay` continue to pass unmodified.

---

## F3 — `internal:wallet:debit` endpoint against `real` (P0)

### Problem

`internal:wallet:credit`/`internal:wallet:debit` scopes exist and are seeded (`OPERATIONS.md:34-41`), but
`router.go:69-74` wires them only to the sandbox wallet (`CreditSandbox`/`DebitSandbox`). There is no route or
service method that lets an M2M client (`ctech-billing`) debit a user's `real` wallet. This is the hard blocker
for `ctech-billing` — its `PLAN.md` is explicitly gated on it.

### Design decisions to confirm before implementing (ask if not obvious from ctech-billing's spec)

- **Route/method naming**: `POST /internal/wallet/real/debit`, mirroring the existing
  `/internal/wallet/sandbox/{credit,debit}` shape, scoped to `internal:wallet:debit` (already seeded, currently
  only enforced against sandbox — needs to also gate this new route).
- **Idempotency key**: caller-supplied `charge_id` from `ctech-billing`, same pattern as `txid`/`round_id` for
  other internal routes (root `CLAUDE.md` invariant #3).
- **Insufficient balance**: fail closed — `409 insufficient-balance` (invariant #1), no partial debit. Whether
  `ctech-billing` retries, dunning, or cancels the subscription on this response is `ctech-billing`'s decision,
  not this service's; the wallet only needs to report the failure correctly and not leave the ledger in an
  ambiguous state (no lock/debit attempted beyond the standard conditional write — there is no PIX call on this
  path, so there is no `processing`/reconciliation state to manage here, unlike `Withdraw`).
- **Reversal**: `ctech-billing` may need a `internal:wallet:credit` reversal call for a refunded charge — confirm
  whether that's a new endpoint or reuses the existing credit route with a `reversal_of: charge_id` idempotency
  key derivation. Recommend the latter (reuse) unless `ctech-billing`'s semantics need something the credit route
  doesn't already support.
- **No new invariant**: this route moves money debit-side out of `real` exactly like a withdrawal does, minus
  the PIX transfer — same lock/idempotency/ledger rules apply, no new financial-safety exception.

### Acceptance criteria

- Unit + integration tests per the standard table (fee: n/a here, no fee on this path; ledger/balance;
  idempotency; lock/concurrency — all required per root `CLAUDE.md`'s Testing section).
- `TestSandboxPurchaseNeverDebitsRealWallet`-style regression: confirm the new route can debit `real` but the
  sandbox routes still cannot.
- Scope seeding in `ctech-account` (`internal:wallet:debit` must clamp to real-wallet routes for
  `ctech-billing`'s client, not just sandbox) — cross-project change, coordinate with `ctech-account`.

---

## F4 — silent lock-backend fallback (P1)

### Problem

`lock.NewLocker` (`api/internal/lock/lock.go:36-42`) silently returns an in-memory locker whenever the cache
backend isn't Valkey/Redis. `cdk/lib/api-stack.ts` resolves `VALKEY_URL` from SSM with a fallback to empty string
on any read failure. The health check reports a degraded cache as `warn` (HTTP 207), and the ALB target group
treats `200,207` as healthy — a degraded instance stays in rotation with per-wallet locking silently
non-shared across the fleet (invariant #4 breaks per-instance, not per-fleet).

### Fix

- In production (`config.Env == "prod"` or equivalent), refuse to boot without a resolved `VALKEY_URL` — same
  fail-closed pattern already used for `SERVICE_AUDIENCE`/`CTECH_URL` (`config.go:63-73`). No in-memory lock
  fallback in prod, full stop.
- If a hard boot-refusal is judged too aggressive for rollout, the minimum acceptable fix is: health check
  reports the in-memory-lock-in-prod condition as unhealthy (not `warn`), so the ALB pulls the instance instead
  of keeping it in rotation.
- Add a CloudWatch alarm on the health check's cache-component status, independent of the ALB behavior, so this
  pages even if health-check semantics are debated later.

### Acceptance criteria

- Boot in a prod-configured test environment with `VALKEY_URL` unset → process exits non-zero (or health check
  returns unhealthy, per whichever option is chosen) rather than silently starting with in-memory locking.
- Existing dev/local behavior (no Valkey required) is unchanged — this only tightens the prod path.

---

## F5 — CPF gate wording + dead webhook secret (P1)

### Problem

Root `CLAUDE.md` invariant #11 states a deposit credits "after re-querying the charge by `txid`... confirming
amount, status, payer CPF." Inter's re-query does not return payer CPF (`model.go:138-142`); it's sourced from
the webhook body only, authenticated by mTLS at the transport layer, not by any payload signature. The SSM
parameter `interWebhookSecret` (`cdk/lib/constants.ts:121`) is provisioned but read nowhere in the codebase.

### Fix

Two independent changes, do both:

1. **Correct the invariant's wording** in root `CLAUDE.md` — split "amount, status" (re-queried, authoritative)
   from "payer CPF" (webhook-sourced, mTLS-authenticated only) so the doc matches what the code actually
   verifies. Do not weaken the underlying control — just stop claiming a stronger one than exists.
2. **Wire `interWebhookSecret` in as defense-in-depth**: HMAC the webhook body with the SSM secret and verify it
   in the webhook handler (`pix-gateway/cmd/webhook/main.go`) before forwarding the `txid` to `api`. If Inter's
   webhook payload doesn't support a signature header (confirm against Inter's docs before implementing), remove
   the unused parameter instead of leaving it as a control that looks live but isn't — do not leave it in the
   ambiguous state found today.

### Acceptance criteria

- If HMAC is added: a webhook with a missing/incorrect signature is rejected before `txid` forwarding; a test
  covers both the valid and tampered-payload cases.
- If the parameter is removed instead: confirm removal is safe (mTLS remains the sole transport control) and
  update `OPERATIONS.md` to no longer reference it.
- Invariant #11's text is updated regardless of which path is chosen.

---

## F6 — deposit-side reconciliation (P1)

### Problem

`api/internal/services/reconcile.go` only resolves withdrawals stuck in `processing`
(`ReconcileWithdrawals`). There is no equivalent for deposits: a `PixDeposit` row that never receives a webhook
sits in `pending` until a 5-minute TTL deletes it (`services/wallet.go:26`, `model.go:145`), with no fallback
re-query and no cross-check against Inter's own statement/extract.

### Fix

Add a `ReconcileDeposits` job (same cadence as `ReconcileWithdrawals`, `cmd/reconcile`) with two responsibilities:

1. **Sweep pending deposits before TTL**: query `PixDeposit` rows in `pending` approaching expiry (e.g., older
   than 3 of the 5 minutes) and re-query Inter's charge status once before they're lost, exactly like the happy
   path's webhook-triggered re-query — this reuses existing re-query logic, just triggered by a timer instead of
   a webhook.
2. **Statement cross-check**: periodically pull Inter's PIX account statement/extract for the period and diff it
   against confirmed deposits in `wallet_ledger_entries`. Any statement entry with no matching ledger entry is a
   money-received-but-not-credited case — alarm (same `slog` `ALARM` pattern already used for refund/reversal
   failures), do not auto-credit without the existing CPF/amount checks.

### Acceptance criteria

- Integration test: a `PixDeposit` seeded in `pending` with no webhook, past the sweep threshold, gets credited
  by the sweep once Inter's charge shows `paid` (fake PIX client scenario).
- Integration test: a fabricated statement entry with no corresponding ledger entry raises the `ALARM` log line
  (metric-filter-testable) without crediting anything automatically.

---

## F7 — aggregate/velocity deposit limits (P2)

Roadmap item, not blocking launch. When implemented: a daily/monthly aggregate cap on deposits and
`real → game` funding, independent of and in addition to the (not-yet-built) personal gambling limits engine —
this is an AML/COAF-reporting control, not a gambling control, so it applies even with `GAMBLING_ENABLED=false`.
No design decision needed now; flag for a future spec once volume approaches a level where this matters.

## F8 — ledger contra-account for cash-in-transit (P2)

Roadmap item, not blocking launch. Consider adding a "cash at Inter" ledger account that deposits credit and
withdrawals debit, alongside the existing single-sided entries, so reconciliation-against-the-bank becomes a
direct ledger query instead of reconstructed from deposit-confirmed + withdrawal-completed entries. No design
decision needed now.

---

## Cross-cutting: shared-code duplication (from `GENERAL-REPORT.md`)

`gopkg.aoctech.app/api-commons` (in `ctech-go-common`) already exists and `api/` already depends on it for
`dynamo.Base`, `cache`, `problem`, and `jwtverify` (commits `7002c6f`, `93b575d`). Two duplications remain
**within this repo**, both between `api/` and `pix-gateway/` (two separate Go modules):

1. **`api/internal/pix/rpc_types.go` ↔ `pix-gateway/internal/rpc/types.go`** — hand-mirrored wire contract, no
   shared module imports it. This is wallet-specific (not a candidate for org-wide `api-commons`); extract to a
   small local Go module (e.g. `rpc-contract/` at the repo root, its own `go.mod`) that both `api` and
   `pix-gateway` import via a `replace` directive, since they'll always ship together from this monorepo.
2. **`tokenManager`** (`api/internal/kycclient` ↔ `pix-gateway/internal/walletclient/walletclient.go:88-101`) —
   a generic OAuth `client_credentials` fetch+cache, not wallet-specific. This is the better candidate for
   `api-commons` itself (org-wide reuse across `ctech-dfe`/future `ctech-billing`/`ctech-poker` too, per the
   general report). Add it to `ctech-go-common` as a new package, then have both `api` and `pix-gateway` depend
   on `api-commons` (currently `pix-gateway/go.mod` has no `api-commons` dependency at all) and delete both local
   copies.

Do (1) inside this repo; coordinate (2) with whoever owns `ctech-go-common` before implementing, since it's an
org-wide shared module, not wallet-owned.

The CI grep-for-duplicate-code step added in commit `e20cf04` should catch regressions here once these are
extracted — confirm it covers `pix-gateway/` too, not just `api/`.

---

## Observability

Add a CloudWatch metric filter on the literal string `"ALARM"` over the app log group, with an alarm attached.
The `slog` `ALARM` lines already exist for refund/reversal failures (root `CLAUDE.md` invariant #12) and for F6's
new statement-mismatch case — none of them currently page anyone; this makes them do so.

---

## Testing gap (applies across F2, F3, F6)

No test in the suite spins up real concurrent goroutines against DynamoDB-local to prove the lock +
conditional-write combination holds under actual contention (`grep -rn "go func\|WaitGroup"` across test files
returns nothing today) — existing tests simulate contention outcomes by pre-seeding state, not by causing races.
Add, using the existing `api/tests/integration/setup_test.go` harness:

- Concurrent identical `Withdraw` (proves F2's fix).
- Concurrent identical `Credit`/`Debit`.
- Concurrent `FundGame`/`PurchaseSandbox` on the same user.
- Concurrent calls to the new real-wallet debit route (F3), once built.

---

## Rollout order

1. **F2, F3** — both P0, both block real-money traffic from any dependent service. F2 is a self-contained bug
   fix; F3 needs a short design confirmation with `ctech-billing` first (see decisions list above) before
   implementation.
2. **F4, F5, F6** — P1, needed before `ctech-billing`/`ctech-poker` go live, not blocking a wallet-only launch.
   No ordering dependency between them; can proceed in parallel.
3. **Cross-cutting (1): `rpc-contract` extraction** — do alongside F3 (F3 adds new wire messages between `api`
   and `pix-gateway`? No — F3 has no PIX leg. Independent; do whenever convenient, ideally before F6 adds new
   `pix-gateway` re-query call shapes, to avoid extracting after a third hand-sync).
4. **Cross-cutting (2): `tokenManager` → `api-commons`** — coordinate with `ctech-go-common` owner; not
   wallet-blocking, do opportunistically.
5. **F7, F8, observability metric filter** — P2/roadmap, no urgency, pick up when convenient.

F1 requires no further action (already fixed).
