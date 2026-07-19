# Responsible Gambling — Self-Exclusion & Game-Deposit Limits

**Status:** approved 2026-07-19
**Repo:** ctech-wallet (money custodian — per Financial Safety Invariants, limits and
exclusion live with the custodian, not with each game)

## Why

- Legal: online poker in Brazil is a skill game, but counsel requires responsible-gambling
  controls — self-exclusion at minimum — before real-money play.
- `api/internal/api/v1/router.go` already encodes the prerequisite: *"The flag flips only
  once the personal limit engine is live — a user must never reach a gambling wallet with
  no limits configured."* This spec is that engine. After it ships, `GAMBLING_ENABLED`
  may be turned on.
- ctech-poker Phase 5 (real-money mode) needs an M2M activation/status check; this spec
  defines it.

## Scope of enforcement

All real-money gambling surfaces already funnel through
`WalletService.requireActivated` — that stays the single chokepoint.

| Surface | Self-excluded | Limits |
|---|---|---|
| `POST /wallet/gambling/activate` | blocked (409 `self-excluded`) | limits now required in body |
| `POST /wallet/game/deposit` | blocked | enforced per window |
| `POST /internal/wallet/game/hold` (poker buy-in) | blocked (defense in depth) | not enforced (money already ring-fenced) |
| `POST /wallet/sandbox/purchase` | **allowed** — sandbox is play-money; exclusion covers real money | n/a |
| `POST /wallet/game/withdraw` | **always allowed** — reducing exposure is never blocked (existing invariant) | n/a |
| `POST /internal/wallet/game/{hold/:id/release,cashout}` | allowed — settlement of an existing hold must never strand money | n/a |

## Self-exclusion

- `POST /v1.0/wallet/gambling/self-exclude` body `{"period": "30d" | "90d" | "indefinite"}`.
- Fixed periods are irreversible until expiry (checked lazily: `until < now` ⇒ no longer
  excluded; no job needed).
- `indefinite` never auto-expires; revocable only via explicit
  `POST /v1.0/wallet/gambling/self-exclude/revoke` **and** only after 90 days from
  `requested_at`. Earlier revoke ⇒ 409.
- Re-excluding while excluded may only extend (new `until` must be later, or upgrade to
  indefinite); shortening ⇒ 409.
- Audit-log every exclusion and revocation (existing audit repository).

## Game-deposit limits

- Three user-set limits in centavos — daily, weekly, monthly — bounding the **sum of
  real→game deposits** (`game/deposit`) per window. Deposits are the strictest, least
  gameable exposure measure; loss-based limits rejected (gameable via withdrawal timing).
- Coherence: `0 < daily ≤ weekly ≤ monthly`.
- **Mandatory at activation:** `gambling/activate` body gains
  `{"daily_limit": n, "weekly_limit": n, "monthly_limit": n}`. Already-activated users
  without limits are re-gated: `game/deposit` ⇒ 409 `limits-not-configured` until they
  set limits via the PUT below.
- `PUT /v1.0/wallet/gambling/limits` (same three fields):
  - Any decrease applies immediately.
  - Any increase becomes **pending** with `applies_at` = now + 7 days (daily or weekly
    field increased) / now + 14 days (monthly increased). One pending set at a time; a
    new PUT replaces it (re-deriving `applies_at`). Pending auto-applies lazily on the
    next read past `applies_at`; `DELETE /v1.0/wallet/gambling/limits/pending` cancels.
  - A mixed request (some fields down, some up) applies the decreases now and pends the
    increases.
- Windows are **calendar windows in America/Sao_Paulo**: day rolls at local midnight,
  week starts Monday, month is the civil month. Chosen over rolling windows because
  users can reason about them ("meu limite volta segunda-feira").
- Enforcement: `deposited_in_window + amount > limit` ⇒ 409 `deposit-limit-exceeded`
  (problem body says which window and when it resets). Checked for all three windows.

## Data model (User row — mirrors existing per-wallet override pattern)

```go
type SelfExclusion struct {
    Status      string // "" | "excluded"
    Period      string // "30d" | "90d" | "indefinite"
    RequestedAt string // RFC3339
    Until       string // RFC3339; empty for indefinite
}

type GameLimits struct {
    Daily, Weekly, Monthly int64 // centavos; 0 = not configured
    Pending *PendingLimits       // nil when none
}
type PendingLimits struct {
    Daily, Weekly, Monthly int64
    AppliesAt              string // RFC3339
}

type GameDepositCounters struct {
    DayKey   string // "2026-07-19" (America/Sao_Paulo)
    DaySum   int64
    WeekKey  string // ISO week "2026-W29"
    WeekSum  int64
    MonthKey string // "2026-07"
    MonthSum int64
}
```

- Window-key mismatch on read ⇒ that counter is logically zero (reset lazily on next
  increment). No sweeper job.
- Counter increment happens in the same DynamoDB transaction as the game-deposit ledger
  write, with a condition on the current sums so two concurrent deposits cannot both
  pass a nearly-exhausted limit.
- Rejected alternative: deriving sums from the ledger per deposit — more reads, more
  code, and the ledger is not indexed by São Paulo calendar windows.

## Poker/M2M integration

- New route `GET /v1.0/internal/wallet/game/status/:user_id`, scope
  `internal:wallet:game-status`, response:
  `{"activated": bool, "self_excluded": bool, "limits_configured": bool}`.
- This is the status check ctech-poker Phase 5 Task 1 (`IsGamblingActivated`) consumes.
  Poker treats anything but `activated && !self_excluded && limits_configured` as
  not-eligible for real-money buy-in.
- Scope grant for poker's M2M client is seeded in ctech-account (same process as the
  game-hold scopes).

## UI (ctech-wallet/ui)

"Jogo responsável" page:
- Limits form with current values, usage bars for the three current windows
  ("R$ 120 / R$ 500 esta semana"), cooldown warning when a field increases, pending
  change banner with `applies_at` and cancel button.
- Self-exclusion section: period picker, strong confirmation dialog spelling out
  irreversibility, and — while excluded — a banner replacing all deposit/play surfaces
  showing until-when (or "indeterminada").

## Non-goals

- Time/session limits, loss limits, reality checks — future work if counsel requires.
- Blocking sandbox play for excluded users.
- Any poker-side changes (ctech-poker Phase 5 consumes the status route; separate plan).
