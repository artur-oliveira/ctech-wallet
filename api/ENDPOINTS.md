# ctech-wallet API — Endpoint Reference

Go REST API (Fiber v3), base path **`/v1.0`**. The code is the source of truth: handlers in
`internal/api/v1/*.go`, routing in `internal/api/v1/router.go`, business logic in `internal/services/wallet.go`.

The wallet is a **ledger**. It keeps two balances (`real`, `sandbox`) and moves Pix through **ctech-billing**, which owns
the Inter connection: a deposit is a Billing invoice, a withdrawal is a Billing payout. The wallet sells nothing and
knows no gambling rule (Poker owns consent, personal limits, self-exclusion and the SKUs). Spec:
`docs/specs/2026-10-14-wallet-ledger-billing-rail.md` in `ctech-billing`.

Auth model (not multi-tenant):

- **User routes** — `Authorization: Bearer <user JWT>` (RS256, JWKS from `ctech-account`). Claims consumed: `sub`
  (user_id), `kyc_level` (`""|basic|enhanced`), `last_mfa_at`, `sid` (empty ⇒ M2M). User route groups require a non-empty
  `sid`; client-credentials tokens are rejected (`middleware/auth.go`, `middleware/claims.go`).
- **Delegated user scopes** — a user token carrying any `wallet:*` is narrowed to the exact capability assigned to the
  route. The Wallet SPA requests every public scope; previously issued first-party tokens without `wallet:*` keep their
  access. `GET /v1.0/ws` uses `wallet:balances:read` because it streams the same balance state.
- **Internal routes** — `client_credentials` M2M token carrying the scope of the route; a non-empty `sid` is rejected even
  if the scope matches (`middleware/scope.go`).
- **Billing's event route** — `POST /internal/billing/events` is authenticated by an HMAC signature, not a token.

All errors are RFC 7807 Problem JSON (`problem/*`); never raw errors or `fiber.Map`.

---

## 1. Health & realtime (unauthenticated)

| Method | Path                 | Notes |
|--------|----------------------|-------|
| GET    | `/v1.0/health`       | Liveness. `{status:"pass",releaseId,serviceId}`. Dependency-free. |
| GET    | `/v1.0/health-check` | Detailed health check (go-common `health`, `application/health+json`). Returns **200** or **207** (degraded) or `503`; HAProxy routing uses the dependency-free `/v1.0/health`, not this endpoint. DynamoDB is the only dependency that can make it `503`; cache and JWKS degrade to `warn`. There is no Inter or Pix check: Billing owns that connection. |
| GET    | `/v1.0/ws`           | WebSocket upgrade. **Auth is the first post-upgrade text frame** (`{"token":"<jwt>"}`), never a `?token=` query parameter. Origin policy mirrors CORS. Fans `deposit_confirmed` and `withdraw_*` events to the user's connections. On shutdown or restart the server closes it with code 1001 (going away): reconnect. |

---

## 2. Caller state (Bearer user JWT)

| Method | Path                               | Behaviour |
|--------|------------------------------------|-----------|
| GET    | `/v1.0/auth/me`                    | `Me{user_id, terms_addendum_accepted, terms_addendum_version}`. The flag is **computed** against the current version constant, never stored, so bumping a version re-gates everyone. The UI gates the app on it. |
| POST   | `/v1.0/auth/terms-addendum/accept` | Records acceptance of the **current** terms addendum. `204`. |

---

## 3. Wallet user routes (Bearer user JWT)

| Scope | Operations |
|---|---|
| `wallet:state:read` | Wallet-side caller state (`GET /auth/me`). |
| `wallet:terms:write` | Accept the Wallet terms addendum. |
| `wallet:balances:read` | Read balances; connect to the balance-update WebSocket. |
| `wallet:ledger:read` | Read statements (`real`, `sandbox`) and the history of old Sandbox-pack and product purchases. |
| `wallet:deposits:write` | Open a Pix deposit. |
| `wallet:withdrawals:write` | Request a withdrawal. |

Scopes add a permission gate; they never bypass KYC, idempotency, ownership or the financial invariants.

| Method | Path                              | Gates | Behaviour |
|--------|-----------------------------------|-------|-----------|
| GET    | `/v1.0/wallet/`                   | — | `{real, [sandbox], limits}`. `real` is always present (auto-created on first access). `sandbox` appears when a Sandbox wallet exists, as history. `limits` is display metadata for the Pix dialogs (centavos): `deposit{min,max,daily_cap,daily_remaining,max_now}`, `withdraw{min,daily_cap,daily_remaining,count_remaining,max_now}`, `resets_at`; `max_now` already folds in today's headroom and the balance (`0` = not possible now). Enforcement stays in the services. |
| GET    | `/v1.0/wallet/:type/ledger`       | — | Paginated statement for `type ∈ {real, sandbox}` (a retired `game` wallet is read-only data and no longer offered). `limit` (default 50, max 200) + opaque `cursor` (a cursor naming another wallet is `400`). Newest first. Item: `{entry_id, wallet_id, type, amount, balance_before, balance_after, ref?, description?, created_at}`; `balance_before` is derived. Response `{items, next_cursor, has_next}`. |
| GET    | `/v1.0/wallet/sandbox/purchases`  | `wallet:ledger:read` | Read-only, newest-first history of the caller's old Sandbox-pack purchase records. Poker sells packs now (through Billing); nothing writes these rows. `limit` and `cursor` as above. |
| GET    | `/v1.0/wallet/product-purchases`  | `wallet:ledger:read` | The same for old digital-product purchases. |
| POST   | `/v1.0/wallet/deposits`           | KYC verified, `Idempotency-Key` | `{amount:int64>0}` ⇒ `201 {txid, amount, status, pix_copia_e_cola, qr_code_base64?, expires_at}`. Opens a **Billing invoice** (customer ensured from the KYC identity, reference = the deposit's `txid`). Range and daily cap are checked **before** the invoice is opened; a replay of the key returns the same txid/QR. If Billing is down the call fails closed (`502`) and leaves no row that blocks a retry. Errors: `422 deposit-out-of-range`, `422 amount-above-limit`, `409 daily-deposit-limit`, `409 idempotency-conflict`. |
| POST   | `/v1.0/wallet/withdrawals`        | KYC verified + step-up MFA, `Idempotency-Key` | `{amount:int64>0}`. No destination field: the payout goes to the KYC CPF, through a **Billing payout**. Debits exactly `amount` (no fee). `201` completed, `202` still `processing` (Billing has only accepted it; the reconcile job and Billing's `payout.done`/`payout.failed` events resolve it). Errors: `403 step-up-required`, `409 daily-withdraw-limit`, `409 insufficient-balance`, `409 wallet-busy`, `422 pix-key-not-found` (debit reversed, slot returned). |

> `Idempotency-Key` (≤128 visible ASCII) is enforced by go-common `idempotency` on both routes, per user: no key `400 idempotency-key-required`; a replay within 24 h returns the stored answer with `Idempotent-Replay: true`; same key with another body, or a request still running, `409 idempotency-conflict` / `idempotency-in-progress`; the claim store unreachable `503 idempotency-unavailable` (fail closed). 5xx and 409 answers are not stored, so a retry runs again. The ledger's own `IDEM#` guard still makes the money effect exactly-once.

### Retired routes

These answer **`410 route-retired`** (after the normal 401 for an anonymous caller, so retirement does not leak which
routes exist). They are listed, and checked, in `router_test.go`.

- Sale of Sandbox packs and goods (moved to Poker and Billing): `POST /wallet/sandbox/purchase`, `POST /wallet/sandbox/purchases`,
  `POST /wallet/sandbox/purchases/:id/refund`; internal `/wallet/sandbox-purchase*`, `/wallet/product-purchase*`.
- The charge and the Pix gateway: `POST|GET /internal/wallet/charge*`, `POST /internal/pix/confirm-deposit|confirm-sandbox-purchase|confirm-product-purchase`.
- The game wallet and the gambling rules (merged into `real`; Poker owns the rules): `POST /wallet/game/withdraw`, `POST /wallet/game/deposit`,
  `/wallet/gambling/*` (activate, limits, self-exclude), `/internal/wallet/game/*`, `GET /internal/wallet/holds/status/:user_id`,
  `GET /internal/wallet/responsible/export`.

---

## 4. Internal routes

> `description` (3-255 chars) is **required** on `sandbox/credit`, `sandbox/debit`, `real/debit` and `holds/cashout` when the
> wallet runs with `REQUIRE_DESCRIPTION=true`; otherwise optional. Hold and release are exempt.

| Method | Path                                          | Scope / auth | Body | Behaviour |
|--------|-----------------------------------------------|--------------|------|-----------|
| POST   | `/v1.0/internal/billing/events`               | HMAC: `X-Billing-Signature` + `X-Billing-Timestamp` (no JWT) | Billing's event envelope | The `invoice.paid` wake-up and the payout events. The wallet reads only the event `type` and the invoice `reference`, then **re-reads the invoice from Billing** and applies its own rules before it credits (Invariants 11, 12). A bad signature or a stale timestamp is `401`; an unknown reference is `200` so Billing stops retrying; two events for one deposit credit once. Mounted before the `/internal` JWT check. |
| POST   | `/v1.0/internal/wallet/sandbox/credit`        | `internal:wallet:credit` | `{user_id, amount>0, idempotency_key, reason?}` | Grants Sandbox credits (a pack Poker sold, a bonus). `CreditSandbox`. |
| POST   | `/v1.0/internal/wallet/sandbox/debit`         | `internal:wallet:debit` | same | Spends Sandbox credits (a bet). **Sandbox-only**. |
| POST   | `/v1.0/internal/wallet/real/debit`            | `internal:wallet:debit-real` | same | Debits the **real** wallet for an authorized M2M client (a table fee, a subscription). Deliberately separate from `:debit` so a Sandbox-only client can never touch `real`. |
| POST   | `/v1.0/internal/wallet/holds/`                | `internal:wallet:hold` | `{user_id, amount>0, table_ref, idempotency_key}` | Reserves a buy-in against `real` (conditional debit, Invariant 1). **Refused while a self-exclusion recorded here is active** (fail closed); consent, limits and exclusion are otherwise Poker's and are checked before it calls. The hold record never bounds the later cash-out. |
| POST   | `/v1.0/internal/wallet/holds/:hold_id/release`| `internal:wallet:hold` | `{user_id, idempotency_key}` | Refunds a `held` hold in full. `user_id` must match the owner. Idempotent. A hold opened on the retired game wallet is refunded to `real`. |
| POST   | `/v1.0/internal/wallet/holds/cashout`         | `internal:wallet:hold` | `{user_id, amount>0, table_ref, hold_ids[], idempotency_key, description?}` | Fail-closed settlement: the amount may not exceed the referenced held value. The credit, the guard and every `held→settled` transition commit atomically. Never refused for an excluded player. |
| GET    | `/v1.0/internal/wallet/balance/:user_id`      | `internal:wallet:balance` | — | Read-only `{game_balance, sandbox_balance}` (centavos): `game_balance` is the **real** balance, what a game can stake. Never creates a wallet. |
| GET    | `/v1.0/internal/erasure/eligibility/:sub`     | `internal:wallet:erasure-eligibility` | — | Account-deletion pre-check, called by **ctech-account**. `{eligible, blockers[]}`. Codes: `wallet.balance_nonzero`, `wallet.hold_open`, `wallet.deposit_pending`, `wallet.deposit_quarantined`, `wallet.withdrawal_pending`, `wallet.purchase_pending` (old rows only). Unknown `sub` ⇒ eligible; nothing is created. |

> **Scope naming.** The wallet's **own** M2M client requests `internal:account:kyc` from `ctech-account` to read the verified CPF
> (`kycclient/kycclient.go`) and `billing:customers:*`, `billing:invoices:*`, `internal:billing:payouts-*` from Billing: different
> scopes on different services. The scopes of the retired routes stay in the manifest as `deprecated` (the registry refuses to drop an
> active scope): `wallet:sandbox-purchases:*`, `wallet:product-purchases:read`, `wallet:game:write`, `wallet:gambling:*`,
> `internal:wallet:confirm-deposit`, `:sandbox-purchase`, `:product-purchase`, `:charge-amount`, `:game-hold`, `:game-cashout`,
> `:game-status`, `:responsible-export`.

> **Account deletion lock.** While ctech-account's deletion saga holds a user (grace, purging or erased), every operation that opens new
> exposure for that `user_id` answers `409 /problems/account-erasure-pending`, on user AND internal routes (`sandbox/credit|debit`,
> `real/debit`, `holds`, deposits). Settlement is never refused: `holds/:id/release`, `holds/cashout` and the Billing events. A deposit paid
> for an ERASED user is refunded to the payer, never credited. User routes that change money or consent also re-check the JWT revocation
> list fail-closed and answer `503 /problems/service-unavailable` when it is unreachable.

---

## 5. Money math (all integer **centavos**)

- **Withdrawal** — debits exactly the requested amount. CTech charges no withdrawal fee and writes no fee ledger entry.
- **Pix deposit range** — per-wallet `min_deposit`/`max_deposit` override the defaults `100`/`1000000` (R$1 / R$10.000); the minimum never
  drops below the absolute floor `100`; an incoherent `max<min` widens rather than rejecting. Checked before the invoice is opened.
- **Daily limits** (per wallet, `domain/wallet/daily_limits.go`) — deposit cap R$1.000, withdraw cap R$1.000, withdraw count 1; counters live on
  the user row, written under the real wallet lock in the same transaction as the movement. The deposit cap is enforced at credit (the excess is refunded).
- **Absolute inbound ceiling** `MaxInboundAmount = 1_000_000 × 100` (R$1.000.000) — a hard cap on a single deposit.
- **Sandbox** `SandboxCreditsPerCent = 100` (R$1 ⇒ 10000 credits), from `rpc-contract/money.json` — used only to display and to price packs in Poker.

---

## 6. Financial Safety Invariants — how each is enforced

1. **Balance never negative** — every debit carries `balance >= :amount` in `balanceTx` (`repositories/wallet.go`). Failure ⇒ `insufficient-balance`.
2. **Ledger append-only** — `ledger_entries` is written only via `BuildPutTxItemIfAbsent`; the authoritative balance is `wallets.balance`.
   `wallet_audit` likewise.
3. **Idempotent** — guard item `IDEM#{key}` written `attribute_not_exists` in the same `TransactWriteItems`; a replay returns the prior result
   and a payload-hash drift is `idempotency-conflict`. Guard rows are permanent.
4. **One op per wallet** — Valkey `SETNX` lock, `LockTTL = 10s`. Contention ⇒ `wallet-busy`.
5. **Cross-wallet lock order** — `lock.AcquireOrdered` sorts keys lexicographically.
6. **Sandbox never becomes real, real never becomes Sandbox** — the list of writing routes is closed (`TestNoRouteConvertsSandboxToAnything`);
   `TestSandboxHasNoRealDebitPath`, `TestCreditingSandboxNeverDebitsReal` and `TestDebitingRealNeverCreditsSandbox` pin the services.
7–10. *(Retired 2026-10-15: the ring-fence rules, the `game` wallet, gross-inflow limits and consent moved to Poker.)*
11. **A Billing event is never the source of truth** — `ConfirmDeposit` re-reads the invoice from Billing before crediting; the pending-deposit
    sweep is the backstop for a lost event.
12. **Attribution before credit** — a paid deposit credits only when the amount equals the expected amount, the payer matches the KYC CPF by the
    wallet's own check **and** Billing says `payer_match = match`. Any disagreement quarantines and refunds; no payer yet ⇒ pending (an `ALARM` line is logged on each sweep). Payouts go only to the KYC CPF.
13. *(Retired 2026-10-07 — was the BaaS verification-fee rule.)*
14. **No money in limbo** — a `processing` withdrawal is resolved by the **reconcile** job from Billing: done ⇒ complete; not found after the grace
    period or failed ⇒ reverse (credit back); a failed reversal ⇒ `refund_failed` + an `ALARM` log line (no SNS alert). A lookup that errors is never read as "not found". Stale
    `held` holds alarm only, never auto-release.

---

## 7. Known divergences

Closed, kept here only so old references resolve:

- **B1** (`dynamodb:TransactWriteItems` in IAM): false positive. It is not an IAM action; the item-level permissions are granted.
- **B7** (Valkey in prod): `config.Load` and `newCacheBackend` fail closed in prod on a missing URL or a connect failure; only non-prod
  falls back to in-memory.
- **B18** (money constants): `SANDBOX_CREDITS_PER_CENTAVO` (100) and `MAX_AMOUNT_CENTS` live in `rpc-contract/money.json` and are pinned
  by a Go test and a UI test. No fee constants exist.
