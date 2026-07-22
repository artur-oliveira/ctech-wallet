# Poker Integration Fixes: M2M Balance Read + Sandbox Activation Decouple — Design

**Status:** proposed
**Date:** 2026-07-22
**Reported by:** `ctech-poker` integration, two issues found integrating against `ctech-wallet`.
**Depends on:** `docs/specs/2026-07-12-three-wallet-topology-design.md` (three-wallet topology,
`ActivateGambling`, `requireActivated`).
**Amends:** invariant #10 language in root `CLAUDE.md` and `api/CLAUDE.md` (see Docs section).

---

## Problem 1 — no M2M way to read balance

`ctech-poker` wants to show a user their `game` + `sandbox` balance so they have visibility into how much
money/credits they hold. No internal M2M route today returns balance for any wallet type.
`GET /internal/wallet/game/status/:user_id` (scope `internal:wallet:game-status`) exists but returns
activation/self-exclusion/limit eligibility (`GameEligibility`, `internal/services/responsible.go:229`),
not balance. `real` is out of scope — poker never touches real money directly.

## Problem 2 — sandbox credit/debit fails `gambling-not-activated`

`ctech-poker`'s daily-reward flow calls `POST /internal/wallet/sandbox/credit` / `.../debit`
(`CreditSandbox`/`DebitSandbox`, `internal/services/wallet.go:768-775`). Both route through `sandboxOp`
(`wallet.go:777-788`), which calls `requireActivated` (`wallet.go:637-648`) — the same gate used by
real-money `game` operations (`FundGame`, `ReturnFromGame`, `PurchaseSandbox`, hold/cashout/status).
`requireActivated` fails closed (`409 gambling-not-activated`) unless `real`, `game`, AND `sandbox` all
already exist, which today only happens together, atomically, inside `ActivateGambling`
(`wallet.go:122-128`) — gated on verified KYC + the gambling addendum
(`docs/specs/2026-07-12-three-wallet-topology-design.md:72,78`).

`sandbox` holds no real money and never converts back to real money (invariant #6). Gating a virtual-credit
daily reward behind KYC/gambling-consent, the same gate that protects real-money exposure, is stricter than
the risk requires: a user should be able to play with sandbox credits (a normal game, no stakes) before ever
deciding to fund `game` with real money.

## Solution

### 1. New M2M balance endpoint

- New route `GET /internal/wallet/balance/:user_id` in a new `internal.Group("/wallet/balance")`
  (`internal/api/v1/router.go`, alongside the existing `/wallet/game` group at router.go:79-96).
- New scope constant `ScopeWalletBalance = "internal:wallet:balance"` (`internal/middleware/scope.go:27`
  area) — read-only, reports `game` + `sandbox` only, never `real`.
- New service method, read-only, no wallet creation:
  ```go
  func (s *WalletService) BalancesFor(ctx context.Context, userID string) (gameBalance, sandboxBalance int64, err error) {
      _, game, sandbox, err := s.repo.LoadWallets(ctx, userID)
      if err != nil {
          return 0, 0, err
      }
      if game != nil {
          gameBalance = game.Balance
      }
      if sandbox != nil {
          sandboxBalance = sandbox.Balance
      }
      return gameBalance, sandboxBalance, nil
  }
  ```
- Response struct declared next to the method (matches the `GameEligibility` pattern,
  `responsible.go:229-233`), not in `dto.go` (which holds only request structs):
  ```go
  type WalletBalances struct {
      GameBalance    int64 `json:"game_balance"`
      SandboxBalance int64 `json:"sandbox_balance"`
  }
  ```
- Handler mirrors `gameStatus` (`internal/api/v1/internal.go:111-117`): parse `user_id`, call service,
  `sendProblem` on error, else `c.JSON`.
- A user with no wallets at all (never touched the wallet) → `{0, 0}`, not an error. This is a legitimate
  state, not a data leak — no PII, no activation/consent info in the response.

### 2. Decouple `sandbox` from gambling activation

- `sandboxOp` (`wallet.go:777-788`): replace the `s.requireActivated(ctx, userID)` call with a lazy,
  no-KYC sandbox-only ensure, reusing the existing generic repo helper:
  ```go
  func (r *WalletRepository) EnsureSandboxWallet(ctx context.Context, userID string) (*wallet.Wallet, error) {
      m, err := r.EnsureWalletsOfType(ctx, userID, wallet.TypeSandbox)
      if err != nil {
          return nil, err
      }
      return m[wallet.TypeSandbox], nil
  }
  ```
  mirroring the existing `EnsureRealWallet` wrapper (`wallet.go:117-127`) over the same generic
  `EnsureWalletsOfType` (`repositories/wallet.go:129-155`) — no new repo logic, just a second named call.
- `FundGame`, `ReturnFromGame`, `PurchaseSandbox`, and the game-hold/cashout/status handlers are
  **unchanged** — all keep calling `requireActivated`, still require full KYC + gambling-addendum consent.
  `PurchaseSandbox` still needs `game` (real money) to exist and is correctly unaffected by this change.
- No `wallet_audit` entry is written for lazy sandbox creation — it is not a consent event, only `wallet_audit`-worthy
  events (`ActivateGambling`, limit changes) remain audited (invariant #10).
- Effect on `DebitSandbox` when sandbox has just been lazily created (balance 0) and the debit amount is
  positive: the conditional debit's `balance >= :amount` fails → `409 insufficient-balance`, not
  `gambling-not-activated`. This is the correct error for "spend more sandbox credits than you have."

### 3. Docs to update (this reverses stated policy, not just an implementation detail)

- `docs/specs/2026-07-12-three-wallet-topology-design.md:72,78` — sandbox no longer requires gambling
  activation/KYC to exist; only `game` does. Sandbox may now be created either by `ActivateGambling`
  (alongside `game`) or lazily by the first M2M sandbox credit/debit, whichever happens first.
- Root `CLAUDE.md` invariant #10 — currently: *"`game`/`sandbox` do not exist until the user accepts the
  gambling addendum... with verified KYC."* Narrow to `game` only; sandbox creation is no longer part of
  this invariant.
- `api/CLAUDE.md` "The gambling ring-fence" section — same correction, plus the new scope added to the
  M2M scope table.
- `ENDPOINTS.md` — document the new `GET /internal/wallet/balance/:user_id` endpoint and its scope.

## Testing

| Case | Type |
|------|------|
| `CreditSandbox`/`DebitSandbox` succeed for a user who never activated gambling (no `real`/`game` rows) | Integration (regression for the reported bug) |
| `FundGame`/`ReturnFromGame`/`PurchaseSandbox`/hold/cashout/status still `409 gambling-not-activated` pre-activation | Integration (proves the `game` gate is untouched) |
| `DebitSandbox` on a freshly lazy-created (zero-balance) sandbox wallet → `409 insufficient-balance` | Unit |
| Repeated `CreditSandbox`/`DebitSandbox` calls don't duplicate the sandbox wallet row (idempotent ensure) | Unit |
| Balance endpoint: zero-state for a brand-new user, correct values after a sandbox credit and after a game fund, `403` without the scope | Integration |
| `TestSandboxPurchaseNeverDebitsRealWallet` still green, unmodified | Regression (existing test, no change expected) |