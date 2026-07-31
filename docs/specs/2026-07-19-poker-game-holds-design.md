# Real-Money Hold/Capture for Skill-Game Integration (poker) — Design

**Status:** proposed
**Date:** 2026-07-19
**Depends on:** `docs/specs/2026-07-12-three-wallet-topology-design.md` (the `game` wallet this spec builds
on top of).
**Blocks:** `ctech-poker` Phase 5 (real-money mode) — see `ctech-poker/ARCHITECTURE.md` § 4 and
`ctech-poker/PLAN.md` Phase 5, both of which name this as a hard prerequisite ("Prerequisite A").

---

## Problem

`ctech-poker`'s real-money mode needs to reserve a player's buy-in the moment they sit at a real-money
table, then settle the actual result when they leave — not debit-on-join and credit-on-leave as two
independent, unrelated transfers. A crash between "chips awarded at the table" and "wallet balance
updated" must never lose or duplicate real money.

Today `ctech-wallet` exposes only unconditional M2M debit/credit routes (`internal/wallet/sandbox/credit`,
`internal/wallet/sandbox/debit`, `internal/wallet/real/debit`) — no reservation primitive exists for any
wallet. There is also no internal route at all against the `game` wallet (only the user-facing
`game/deposit` / `game/withdraw` transfer routes exist, gated behind the caller's own JWT).

`ctech-poker/ARCHITECTURE.md` § 4 proposes a classic payment-processor-shaped hold/capture API
(`POST /v1/holds`, `POST /v1/holds/{id}/capture`, `POST /v1/holds/{id}/release`) where `capture_amount`
is bounded by the original hold. **That shape does not fit poker.** A player's final stack when they
leave a table is not bounded by their own buy-in — it also contains chips won from other seated
players' buy-ins. Modeling cash-out as "capture ≤ this player's own hold" is simply wrong for a game
where money is redistributed among holds at the same table, not returned to the same party that put it
up. The design below keeps the hold/release vocabulary for the parts where it's correct (buy-in
reservation and full-refund abort). The original unbounded cash-out proposal is superseded by the
security remediation below.

## Solution

Three new M2M-only routes against the `game` wallet, following the exact shape of the existing
`sandboxCredit`/`sandboxDebit`/`realDebit` internal handlers (`api/internal/api/v1/internal.go`) and the
`sandboxOp`/`DebitReal` service methods (`api/internal/services/wallet.go`) — no new transport or auth
pattern, only a new resource (`Hold`) and three new service methods.

### Generality — this is not poker-specific

Root `CLAUDE.md` already frames `game` as the base "for subscription billing **and skill-game (poker/dominó)
integration**" — poker is the first consumer, not the only intended one. The hold/release/cashout shape below
is deliberately game-agnostic: `table_ref` is an opaque caller-supplied string (any skill game's own session
identifier), and nothing in the wallet-side logic assumes poker's rules. Building this as a shared `game`
wallet capability — reviewed once, under wallet's own Financial Safety Invariants — is the reuse-over-reinvention
principle this company already applies to shared infra (`ctech-go-common/lock`, `jwtverify`): a second skill
game integrating later reuses this contract instead of each game re-earning its own real-money-safety review.

### Why the reservation stays in `ctech-wallet`, not poker's own ledger

The money-custody principle: only the system that holds money should be the one that reconciles it. Poker (or
any future skill game) is authoritative over *game outcomes* (hand results, side pots) — it is not, and should
not become, authoritative over *whether real money is safely accounted for*. Two independent failure modes
exist and this split covers both without overlap:

- **A single call to wallet fails in flight** (network blip, timeout) — this is the calling game service's own
  problem to retry until confirmed; `ctech-poker`'s Phase 5 plan already builds this (its own durable
  pending-credit tracking, Task 4) and every future skill game will need the equivalent, same as any client of
  any at-least-once API.
- **The calling game service itself never comes back** (crashes, is decommissioned, has a bug in its own
  reconciliation, or is compromised) — no amount of poker-side retry logic detects this, because the thing
  that's supposed to retry is the thing that's broken. Only the money-holder can independently notice "funds
  reserved, nobody ever claimed them back" — which is exactly what the stale-hold sweep below provides, and
  exactly why it must live in `ctech-wallet`, not in poker's own store. This is defense in depth that matters
  specifically because this is real money under an unresolved legal posture (OVERVIEW.md § 11) — the wallet
  should not have to fully trust every calling game service's own crash-recovery to protect a player's balance.

These two mechanisms are complementary, not duplicated effort — a plain unconditional `game/credit`+
`game/debit` contract (the alternative considered and rejected here) would still require poker to build its
own Task 4-equivalent for the first failure mode, while leaving the second failure mode with no independent
detection at all. The extra surface a `Hold` record adds (one new table, one new status machine) buys that
missing coverage; it is not solving the same problem poker's own reconciliation already solves.

### Why `game`, not `real`

Invariant #7 (root `CLAUDE.md`): real money enters the gambling ring-fence only via `real → game`. Poker
is a gambling surface — its real-money holds and cash-outs must operate against `game`, exactly like
`DebitSandbox`/`CreditSandbox` already do for the sandbox ledger. `HoldGame`/`ReleaseHold`/`CashoutGame`
all require `requireActivated` (same as every existing game/sandbox operation) — a user who never
activated gambling cannot be held against, full stop.

### New domain type

```go
// api/internal/domain/wallet/model.go additions

const (
HoldHeld = "held"
HoldReleased = "released"
HoldSettled  = "settled" // consumed by a cash-out credit
)

const (
EntryGameHoldDebit = "game_hold_debit"         // buy-in reservation
EntryGameHoldRelease = "game_hold_release"     // full refund, table/hand aborted before play
EntryGameCashoutCredit = "game_cashout_credit" // final stack credited back on leaving the table
)

const TableHolds = "wallet_holds"

// Hold is an open reservation against a player's game wallet, created at
// buy-in. It never bounds the eventual cash-out amount — poker's own table
// ledger is authoritative for how much a player's stack is worth when they
// leave; this record exists for idempotency, audit, and stale-hold detection.
type Hold struct {
HoldID         string `dynamodbav:"pk" json:"hold_id"`
WalletID       string `dynamodbav:"wallet_id" json:"wallet_id"`
UserID         string `dynamodbav:"user_id" json:"user_id"`
Amount         int64  `dynamodbav:"amount" json:"amount"`       // original reservation, centavos
TableRef       string `dynamodbav:"table_ref" json:"table_ref"` // opaque caller reference (e.g. table_id:seat)
Status         string `dynamodbav:"status" json:"status"`
IdempotencyKey string `dynamodbav:"idempotency_key" json:"-"`
CreatedAt      string `dynamodbav:"created_at" json:"created_at"`
UpdatedAt      string `dynamodbav:"updated_at" json:"updated_at"`
}
```

`wallet_holds` is a new table, same shape/GSI conventions as `wallet_withdrawals` (a `gsi_status` index
for the stale-hold sweep, see below).

### API surface

| Route                                                    | Scope                          | Body                                                             | Effect                                                                                                                                                                                                                                                                |
|----------------------------------------------------------|--------------------------------|------------------------------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `POST /v1.0/internal/wallet/game/hold`                   | `internal:wallet:game-hold`    | `{user_id, amount, table_ref, idempotency_key}`                  | Debits `game` by `amount` (same `ConditionExpression: balance >= :amount` as every other debit — insufficient real money in the ring-fence is a normal `409`, not a special case). Creates a `Hold` row, `status=held`. Ledger entry `game_hold_debit`.               |
| `POST /v1.0/internal/wallet/game/hold/{hold_id}/release` | `internal:wallet:game-hold`    | `{idempotency_key}`                                              | Only valid on a `held` hold. Credits `game` by the **full original amount**, marks the hold `released`. For a table/hand that never played (e.g. player leaves before any hand starts) — a plain refund, no poker-side settlement math involved.                      |
| `POST /v1.0/internal/wallet/game/cashout`                | `internal:wallet:game-cashout` | `{user_id, amount, table_ref, hold_ids: [...], idempotency_key}` | Atomically credits `game` and settles the listed holds. Every hold must be `held`, owned by the user and table, unique, and their aggregate must cover `amount`. |

A deliberately separate scope for `hold` vs `cashout` (mirrors the existing `internal:wallet:debit` vs
`internal:wallet:debit-real` separation) — `ctech-poker`'s M2M client is issued both, but the split keeps
IAM/JWT scopes legible if a future skill game only ever needs one side.

### Security remediation: cash-out is bounded until zero-sum settlement exists

An M2M scope is not sufficient authority to mint arbitrary real money. The original per-player cash-out
contract could credit any amount if a game client or credential were compromised. The current implementation
therefore fails closed: a cash-out cannot exceed the aggregate value of that user's listed holds, and credit +
hold settlement occur in one DynamoDB transaction. Supporting poker winners above their own reservations
requires a separate table-wide, zero-sum settlement operation that atomically consumes every participant's
holds and credits every participant's final stack after proving total inputs equal total outputs.

### Idempotency

Same discipline as every other mutation (Invariant #3): `hold` and `cashout` require a bounded
`idempotency_key` and bind it to the full request. Release also requires the field for API compatibility,
but persistence uses a deterministic key derived from the hold ID so no caller key can ever release the
same hold twice. Guards are permanent and credit + hold-state changes share one DynamoDB transaction.

### Stale-hold reconciliation (Invariant #12 analog)

A hold that sits in `held` past a generous ceiling (e.g. 24h — no real cash game session should run
longer) with no `release`/`cashout` ever arriving is exactly the "money left in limbo" case Invariant #12
already names for withdrawals. Mirror that pattern: a `gsi_status` scan (same shape as the withdrawal
reconciliation job) finds holds stuck in `held` past the ceiling and raises an **operational alarm** —
never auto-released, never auto-cashed-out. Auto-releasing a hold `ctech-poker` still considers open
would double-credit the instant poker's own crash-recovery resumes the table and later calls `cashout`
itself. This is a page-a-human case, same as a stuck withdrawal.

### What this does NOT change

- No change to `real`, `sandbox`, or any existing route.
- No change to the personal-limit engine's metering — holds/cash-outs move money that already crossed
  `real → game`; they don't touch the metered edge itself (Invariant #8, gross-inflow-only, is untouched).
- No rake/house-cut mechanics — `ctech-poker/PLAN.md`'s Phase 5 note flags rake as a separate, unresolved
  product question. If/when a rake is adopted, it's poker reducing the cash-out `amount` it sends
  (house's share stays in `game` as the pooled table's un-credited remainder, or is separately debited to
  a house account) — a decision for that spec, not this one.

## Open questions — resolved 2026-07-19

1. **Stale-hold alarm ceiling: 24h**, independent of the withdrawal reconciliation's own sweep interval.
2. **New `wallet_holds` table** — a hold's lifecycle doesn't share `Withdrawal`'s fields; folding it in
   would grow a lot of meaningless `omitempty` fields.
3. **Keep the 1000 RCU/WCU on-demand cap**, same as every sibling table — revisit only if poker load
   testing shows it's insufficient.

## Implementation status

Implemented 2026-07-19: `HoldGame`/`ReleaseHold`/`CashoutGame` service methods, `wallet_holds` repository
(`CreateHold`/`GetHold`/`UpdateHoldStatus`/`ScanStaleHolds`), the three M2M routes under
`/v1.0/internal/wallet/game/{hold,hold/:id/release,cashout}`, the `ScopeWalletGameHold` /
`ScopeWalletGameCashout` scopes, the `SweepStaleHolds` reconciliation job (wired into `cmd/reconcile`),
and the `wallet_holds` CDK table. The 2026-07-31 remediation made release and cash-out atomic and permanently
idempotent, bounded cash-out by owned held value, and added unit + integration regressions against fund minting,
consumed-hold reuse, concurrent double release, and stale-hold auto-resolution.

**Not done here (separate repo/process, tracked as plan task 8):** granting `ctech-poker`'s M2M client
the two new scopes — that grant is seeded in `ctech-account` (per root `CLAUDE.md`'s M2M scope model),
not `ctech-wallet`/`ctech-cdk`. Handing the confirmed request/response shapes back to whoever is building
`ctech-poker`'s Phase 5 integration is also still outstanding.
