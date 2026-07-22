# Three-Wallet Topology + Gambling Activation — Design

**Status:** proposed
**Date:** 2026-07-12
**Supersedes parts of:** `docs/specs/2026-07-10-wallet-design.md` (§A wallet types, §C sandbox purchase)
**Depends on:** nothing. **Blocks:** the personal limit engine, game-service balance access.

---

## Problem

Today a user has two wallets: `real` (PIX in/out, real money) and `sandbox` (virtual, no monetary value).
Real money enters gambling through `POST /wallet/sandbox/purchase` (real → sandbox).

Two requirements do not fit that model:

1. **Responsible-gambling limits.** The user must be able to cap how much of their own money they can
   commit to gambling per day/week/month. There is no ring-fence to meter — `real` is also used for
   subscriptions and services, which must *not* count toward a gambling limit (they are recurring,
   cancellable, and not an addiction vector).
2. **Real money in games.** Games will eventually be played with real money, not only sandbox credits.
   There is no wallet that holds real money earmarked for gambling.

## Solution

Split the ring-fence out of `real` into its own wallet. Three wallet types:

| Wallet    | Money      | Purpose                                     | Limits apply |
|-----------|------------|---------------------------------------------|--------------|
| `real`    | Real       | PIX deposit/withdraw, subscriptions, services | No         |
| `game`    | **Real**   | Real money earmarked for games only          | **Yes**     |
| `sandbox` | Virtual    | Game credits, no monetary value               | Yes (inherited) |

`game` holds **real money** — it is withdrawable (via `game → real → PIX`) and counts toward the user's
real holdings. It is not a separate currency; it is the same centavos behind a gate.

### Money topology

```
                    ┌──────────── subscriptions / services (debit)
                    │
  PIX deposit ──►  real  ◄── PIX withdraw
                    │ ▲
     (LIMITED) ─────┤ │───── return (unlimited)
                    ▼ │
                   game
                    │
                    ▼
                 sandbox ──► game credit/debit (M2M, virtual)
```

**The load-bearing invariant:** *no real money reaches a game or sandbox except by crossing the
`real → game` edge.* That edge is the single choke point where personal limits are enforced. One edge to
audit, one edge to test.

This requires **removing the current `real → sandbox` purchase path**. Sandbox is bought from `game`.
Leaving the direct path in place would make limits decorative: a user at their cap would simply buy
sandbox from `real` instead.

### Gross inflow, never netted

The `real → game` edge is metered as **gross inflow**. A `game → real` return does **not** refund limit
headroom. Otherwise the limit is trivially bypassed: fund R$100 → return R$100 → fund R$100 again, forever,
against a R$100 cap. Returns are always allowed and never restricted — moving money *out* of the ring-fence
reduces exposure, so there is no reason to gate it.

(The counter itself is specified in the limit-engine spec; this spec only fixes *where* it is measured and
that it is gross.)

## Activation (opt-in)

`game` does not exist until the user explicitly activates it. A user who only pays for subscriptions never
sees a gambling surface. **`sandbox` no longer shares this gate** (amended in
`docs/specs/2026-07-22-poker-integration-fixes-design.md`): it is play currency, not real money, so it is
created independently of KYC/gambling consent — either alongside `game` at activation, or lazily on the
first M2M sandbox credit/debit, whichever happens first.

**Wallet creation:**
- `EnsureWallets` creates **`real` only**. This is the change from today (it currently creates
  `real` + `sandbox`).
- `game` and `sandbox` are created together, atomically, at activation — but `sandbox` may already exist by
  then (lazily created by an earlier M2M sandbox op); `EnsureGamblingWallets` reuses it rather than
  replacing it.

**Activation requires all of:**
1. KYC `verified` — real money is about to enter a gambling ring-fence.
2. Acceptance of the **gambling addendum** (a document distinct from the wallet terms addendum).
3. Initial personal limits set in the same request.

**Ordering constraint (important).** Requirement 3 depends on the limit engine, which is the *next* spec.
That leaves two honest options, and they must not be fudged:

- **(a) Ship 2 and 3 together.** Activation is never exposed without limits. Larger single release; no
  window in which a user can be activated with no cap.
- **(b) Ship 2 first with the `real → game` edge and activation behind a feature flag**, off in production
  until the limit engine lands.

Recommend **(b)**: it lets the topology, the migration, and the bypass regression tests land and soak on
their own, while making it structurally impossible for a real user to reach an unlimited gambling wallet.
What must *not* happen is shipping activation enabled with limits "to follow" — that is precisely the state
the feature exists to prevent.

**Mechanism — reuse, do not reinvent.** The existing terms-addendum pattern is exactly right:
`CurrentTermsAddendumVersion` + acceptance as a *computed equality* against that constant (never a stored
boolean), so bumping the constant re-gates every user at once. Add a parallel
`CurrentGamblingAddendumVersion` on the `User` row. Activation is `GamblingAddendumVersion == current`
**and** the `game` wallet existing.

Bumping the gambling addendum re-gates gambling **without** freezing existing balances: the user keeps their
`game`/`sandbox` balances and can still *return* money to `real`, but cannot fund or play until they accept
the new version. Money is never trapped by a terms change.

**Deactivation / self-exclusion** is deliberately out of scope here and belongs with the limit engine, where
the cooldown machinery lives. Noted so it is not forgotten: an activation flow without a self-exclusion
counterpart is incomplete.

## API surface

| Route | Change |
|---|---|
| `POST /v1.0/wallet/gambling/activate` | **New.** Gated on KYC `verified` + gambling addendum + initial limits. Creates `game` + `sandbox` atomically. Idempotent. |
| `POST /v1.0/wallet/game/deposit` | **New.** `real → game`. **Limited.** Requires activation. |
| `POST /v1.0/wallet/game/withdraw` | **New.** `game → real`. Unlimited. Requires activation. Not a PIX payout — an internal transfer. |
| `POST /v1.0/wallet/sandbox/purchase` | **Changed.** Source becomes `game`, not `real`. Requires activation. |
| `GET /v1.0/wallet/balances` | **Changed.** Returns `real` always; `game` and `sandbox` only when activated. |
| `POST /v1.0/wallet/withdraw` | Unchanged — PIX payouts remain `real`-only. |

Naming note: `game/deposit` and `game/withdraw` are namespaced under `/game`, so they do not collide with
the top-level PIX `/wallet/deposit` and `/wallet/withdraw`. "Withdraw" here means *out of the ring-fence*,
not *out of the platform*.

**Frontend:** on first access the wallet shows **only the real balance**. Game/sandbox cards appear only
once activated. Game services will later deep-link users into an activation page — that flow, and the M2M
scope letting game services read balances, is a separate spec.

## Data model

**`Wallet.Type`** gains `game` (`wallet.TypeGame = "game"`).

**New ledger entry types** (append-only, mirroring the existing `sandbox_purchase` / `sandbox_credit` pair):

| Constant | Value | Side |
|---|---|---|
| `EntryGameFundDebit` | `game_fund_debit` | debit `real` |
| `EntryGameFundCredit` | `game_fund_credit` | credit `game` |
| `EntryGameReturnDebit` | `game_return_debit` | debit `game` |
| `EntryGameReturnCredit` | `game_return_credit` | credit `real` |

`sandbox_purchase` / `sandbox_credit` keep their names; only the source wallet changes.

**Sandbox unit.** A sandbox balance is measured in **credits**, a virtual unit with no monetary value — not
centavos. A `game → sandbox` purchase converts the real-money `amount` (centavos) into credits at a **fixed
backend rate**: `R$ 1,00 (100 centavos) = 1000 credits` (`SandboxCreditsPerCentavo = 10`, in
`api/internal/domain/wallet/model.go`; mirror `SANDBOX_CREDITS_PER_CENTAVO` in the UI). The rate is a
constant, never client-supplied. The `sandbox_purchase` debit entry stays in centavos (on `game`); the
`sandbox_credit` entry is in credits (on `sandbox`). This decouples the unit so a credit never reads as real
money (Invariant #6).

**`User`** gains `gambling_addendum_version` + `gambling_activated_at`.

**New table `wallet_audit`** — append-only, non-money events, which the ledger deliberately does not carry:
activation, addendum acceptance, and (next spec) every limit change. Records actor, event type, before/after
where applicable, IP, user-agent, timestamp. This is what makes "every action is auditable" true for actions
that move no money. Same append-only discipline as `ledger_entries`: never updated, never deleted.

## Financial Safety Invariants — impact

Root `CLAUDE.md` needs three amendments. Every one is a tightening, not a relaxation:

- **#4 (one op per wallet)** — unchanged, but now covers three wallets.
- **#5 (lock ordering)** — becomes a three-way fixed order: **`real` → `game` → `sandbox`**. Every
  cross-wallet op takes locks in that order. `real→game`, `game→real`, and `game→sandbox` each touch exactly
  two wallets, so the order is total and deadlock-free.
- **#6 (sandbox never becomes real)** — **unchanged and still absolute.** `sandbox` remains a sink: nothing
  converts sandbox back to `game` or `real`. Add the new companion rule: *real money enters the gambling
  ring-fence only via `real → game`.*

New invariant, to be added as **#9**: *`game` balance is real money.* It is withdrawable (via `real`),
counts toward the user's real holdings, and is never written off or expired. The user's total real money is
`real.balance + game.balance`.

## Migration

Existing users have `real` + `sandbox`, and some hold sandbox balances. They never consented to a gambling
addendum that did not exist.

**Do not grandfather them into activated state** — that would fabricate consent. Instead:

- Existing `sandbox` wallets are left in place and **frozen**: readable, but no purchases and no play until
  the user activates.
- The user is *not* locked out of their own money: sandbox holds no real money by definition, so nothing of
  value is frozen. No `game` wallet is created until activation.
- `EnsureWallets` stops creating `sandbox`; existing rows are untouched.

A backfill script is unnecessary — activation creates what is missing, and `game` is absent until then.

## Testing

| Area | Required |
|---|---|
| `real → game` transfer | Unit + integration: atomic pair, no-negative, idempotent replay |
| `game → real` return | Unit + integration: atomic, never blocked by limits |
| Lock ordering | Integration: concurrent cross-wallet ops → `wallet-busy`, no deadlock |
| Activation | Integration: idempotent; creates both wallets atomically; blocked without KYC `verified`; blocked without addendum |
| **Bypass regression** | Integration: `real → sandbox` is **impossible** — sandbox purchase from a user with no `game` wallet must fail, and must never debit `real` |
| Balances | Integration: `game`/`sandbox` absent from the response before activation |
| Audit | Integration: activation writes a `wallet_audit` row; the row is never mutated |

The bypass regression test is the one that matters most: it is the executable form of the load-bearing
invariant.

## Decisions (resolved 2026-07-12)

1. **No step-up MFA on `real → game`.** PIX withdrawal keeps its `RequireRecentMFA(5m)` gate; funding the
   game wallet does not get one. The money is not leaving the platform, and an MFA prompt on every top-up is
   friction that pushes users toward larger, less frequent — therefore riskier — transfers.
2. **No fee on either game edge.** `real → game` and `game → real` are both free. A fee on returning money
   *out* of the ring-fence would penalise the exact behaviour the feature exists to encourage.
3. **Ship behind a feature flag** (option (b) above): the topology, migration, and bypass regression tests
   land first with activation disabled in production; the flag flips only once the limit engine is live.
