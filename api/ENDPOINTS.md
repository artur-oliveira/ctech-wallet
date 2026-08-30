# ctech-wallet API — Endpoint Reference

Go REST API (Fiber v3), base path **`/v1.0`**. Code is the source of truth; all
anchors are `file:line`. Handlers: `internal/api/v1/*.go`; routing:
`internal/api/v1/router.go`; business logic: `internal/services/wallet.go`.

Auth model (not multi-tenant):

- **User routes** — `Authorization: Bearer <user JWT>` (RS256, JWKS from
  `ctech-account`). Claims consumed: `sub` (user_id), `kyc_level`
  (`""|basic|enhanced`), `last_mfa_at`, `sid` (empty ⇒ M2M). User route
  groups require a non-empty `sid`; client-credentials tokens are rejected. See
  `middleware/auth.go:28`, `middleware/claims.go`.
- **Delegated user scopes** — a user token carrying any `wallet:*` is narrowed
  to the exact capability assigned to the route. The Wallet SPA requests every
  public scope; previously issued first-party tokens without `wallet:*` retain
  their existing access during the migration. A scope from another Resource
  Server does not opt a token into this narrowing. `GET /v1.0/ws` uses
  `wallet:balances:read` because it streams the same balance state.
- **Internal routes** — `client_credentials` M2M token carrying exactly one
  scope per route; a non‑empty `sid` is **rejected** even if the scope matches
  (`middleware/scope.go:39`).

All errors are RFC 7807 Problem JSON (`problem/*`); never raw errors or
`fiber.Map`. See `internal/problem/problem.go` for type URIs.

---

## 1. Health & realtime (unauthenticated)

| Method | Path                 | Handler         | Notes                                                                                                                                                                                                                                                                                                                                    |
|--------|----------------------|-----------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| GET    | `/v1.0/health`       | `health.go:119` | Liveness. Returns `{status:"pass",releaseId,serviceId}`. Dependency‑free.                                                                                                                                                                                                                                                                |
| GET    | `/v1.0/health-check` | `health.go:123` | Detailed draft‑inadarei health check. ALB target group accepts **200 and 207** (degraded). DynamoDB is the only dependency that can fail the probe (`503`); cache/PIX/JWKS degrade to `warn`. `health.go:118`,`aggregate:170`.                                                                                                           |
| GET    | `/v1.0/ws`           | `ws.go:82`      | WebSocket upgrade. **Auth is the first post‑upgrade text frame** (`{"token":"<jwt>"}`), NOT a `?token=` query param (that leaked into LB/CF logs — see `ws.go:74`). Origin policy mirrors CORS (`wsAllowedOrigin:58`). The registry fans `deposit_confirmed` / `withdraw_*` events to the user's connections (`services/wallet.go:489`). |

---

## 2. Auth / caller state (Bearer user JWT)

| Method | Path                               | Handler      | Auth     | Body | Behaviour                                                                                                                                                                                                                                                                                                                                                    |
|--------|------------------------------------|--------------|----------|------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| GET    | `/v1.0/auth/me`                    | `auth.go:11` | user JWT | —    | Returns `Me{user_id, terms_addendum_accepted, terms_addendum_version, gambling_addendum_accepted, gambling_addendum_version}`. Both `*_accepted` flags are **computed** against the current version constants (`user.go:44`, `domain/wallet/user.go:53,60`) — never stored — so bumping a version re‑gates everyone at once. UI gates the whole app on this. Also carries `deposit{allowed, blocked_by, kyc_level, custody_required, custody_status}` — the pre-flight deposit gate (`services/wallet.go` `DepositReadiness`) the dashboard renders its deposit button from. Omitted entirely if the probe errors: absent means "unknown, behave as before", never "blocked". See `../docs/specs/2026-08-29-deposit-gate.md`. |
| POST   | `/v1.0/auth/terms-addendum/accept` | `auth.go:20` | user JWT | —    | Records acceptance of the **current** terms addendum version (partial upsert, never overwrites the gambling acceptance). `204 No Content`. `repositories/user.go:39`.                                                                                                                                                                                        |

---

## 3. Wallet user routes (Bearer user JWT)

Public scope map:

| Scope | Operations |
|---|---|
| `wallet:state:read` | Read Wallet-side caller state (`GET /auth/me`). |
| `wallet:terms:write` | Accept the Wallet terms addendum. |
| `wallet:balances:read` | Read balances and connect to the balance-update WebSocket. |
| `wallet:ledger:read` | Read statements for real, game, and sandbox wallets. |
| `wallet:deposits:write` | Open a PIX deposit. |
| `wallet:withdrawals:write` | Create a PIX withdrawal; KYC and step-up MFA still apply. |
| `wallet:sandbox-purchases:read` | Read sandbox-credit purchase history. |
| `wallet:sandbox-purchases:write` | Buy sandbox credits or refund an eligible purchase. |
| `wallet:product-purchases:read` | Read digital-product purchase history. |
| `wallet:game:write` | Move value `real↔game`; feature/KYC/limit gates still apply. |
| `wallet:gambling:read` | Read responsible-gambling limits and status. |
| `wallet:gambling:write` | Activate gambling and manage limits or self-exclusion. |
| `wallet:custody:write` | Start custody onboarding or account closure. |

Scopes add a permission gate; they never bypass KYC, recent-MFA, feature flags,
idempotency, responsible-gambling limits, ownership, or financial invariants.

| Method | Path                                        | Handler         | Extra gate                                               | Body                                                           | Side‑effects / business rules                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                 |
|--------|---------------------------------------------|-----------------|----------------------------------------------------------|----------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| GET    | `/v1.0/wallet/`                             | `wallet.go:17`  | —                                                        | —                                                              | Returns `{real, activated, [game], [sandbox], [custody_status]}`. **`real` is always present** — the response shape does not change with custody onboarding progress, and `custody_status` is a reporting-only extra a client may ignore. It used to blank all three wallets until the subaccount was approved, which dropped `real` from a response whose contract requires it and stranded `sandbox` (play currency, no custody involvement) behind an unrelated step; whether the caller may deposit is answered by the gate on `GET /auth/me`, never by withholding balances. `activated` and `game` remain tied exclusively to gambling consent. An independently created `sandbox` is returned before activation so its read-only ledger remains available; its presence never implies consent. `real` wallet is auto‑created on first access (`EnsureRealWallet`).                                                                                                                                                                                                                                                                        |
| POST   | `/v1.0/wallet/deposits`                     | `wallet.go:32`  | `RequireKYC(enhanced)` (`router.go:47`)                  | `{amount:int64>0}` (`dto.go:5`)                                | Opens a PIX **static QR code** against the caller's own Asaas subaccount and records a pending deposit. **There is no Inter path** (Invariant #14): without an approved subaccount this is `409 wallet-onboarding`, never a charge in CTech's own account. Gates, in order: approved subaccount → no open MED receivable → monthly receipt allowance (`429 deposit-receipts-exhausted`) → `MaxInboundAmount` → the wallet's deposit range. All of them run **before** any charge is opened. Idempotency key required (`Idempotency-Key` header), registered before the charge so a retry never opens a second QR (SEC‑08); a replay of an unpaid deposit is answered from the stored QR, because a static QR has no payment at the provider until someone pays it. Returns `{txid, amount, status, pix_copia_e_cola, qr_code_base64, expires_at}`. **No balance change yet** — credit happens only at `ConfirmDeposit` after re‑query. See `../docs/specs/2026-08-30-asaas-only-deposits.md`. |
| POST   | `/v1.0/wallet/withdrawals`                  | `wallet.go:57`  | `KYC verified` + `RequireRecentMFA(5m)` (`router.go:45`) | `{amount:int64>0}` (`dto.go:12`)                               | Debits `amount+fee` atomically, then sends a PIX payout to the **CPF on the caller's KYC record** (never a client‑supplied key — anti‑fraud). Destination is verified same‑owner at Inter. Fee per‑wallet / absolute floor (§6). If the CPF has no PIX key ⇒ immediate reverse + `pix-key-not-found`. Any other payout failure ⇒ withdrawal left in `processing` for the reconciliation job (Invariant #12). Returns the `Withdrawal` (status `completed` or `processing`→`202 Accepted`). `wallet.go:546`.                                                                                                   |
| POST   | `/v1.0/wallet/sandbox/purchase`             | `wallet.go:85`  | —                                                        | `{amount:int64>0}`                                             | Converts **game→sandbox** credits (`PurchaseSandbox`). Source is `game`, **never `real`** — real money reaches sandbox only by first crossing `real→game` (Invariant #7). Sandbox is a sink (Invariant #6). Credit = `amount × 10` credits (`model.go:115`). Idempotency key required.                                                                                                                                                                                                                                                                                                                        |
| GET    | `/v1.0/wallet/sandbox/purchases`            | `sandbox_purchase.go` | —                                                 | —                                                              | Read-only, newest-first history of the caller's sandbox-credit purchase records, including M2M-opened purchases. Ownership is enforced by `gsi_user` (`user_id`, `created_at`); supports `limit` (default 50, max 200) and opaque `cursor`. Separate from the sandbox ledger. |
| GET    | `/v1.0/wallet/product-purchases`            | `m2m_product_purchase.go` | —                                             | —                                                              | Read-only, newest-first history of generic digital-product purchases owned by the caller, including M2M-created sales. Queries `gsi_user` and returns no requesting-client/webhook metadata. Supports `limit` and opaque `cursor`; these records have no ledger effect. |
| GET    | `/v1.0/wallet/:type/ledger`                 | `wallet.go:161` | —                                                        | —                                                              | Paginated statement for `type ∈ {real,game,sandbox}`. `game` ⇒ `gambling-not-activated` before consent; an existing independently created `sandbox` remains readable before activation, while an absent sandbox returns the same problem. `limit` (default 50, max 200) + `cursor` (`intQuery`/`decodeCursor`).                                                                                                                                                                                                                                                                                              |
| POST   | `/v1.0/wallet/game/withdraw`                | `wallet.go:125` | —                                                        | `{amount:int64>0}`                                             | **Always registered, never flag‑gated** (`router.go:55`). `game→real` (`ReturnFromGame`): never limited, never charged (Invariant #8). A route **out** of the ring‑fence must always exist so `GAMBLING_ENABLED=false` can never strand a user's own money.                                                                                                                                                                                                                                                                                                                                                   |
| POST   | `/v1.0/wallet/gambling/self-exclude`        | `wallet.go:188` | —                                                        | `{period:"30d"                                                 | "90d"                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                         |"indefinite"}` | Self‑exclusion (reduces exposure ⇒ always available). Extension‑only: a new exclusion may never end earlier than the one it replaces; indefinite cannot be lengthened. Audited. `responsible.go:36`. |
| POST   | `/v1.0/wallet/gambling/self-exclude/revoke` | `wallet.go:202` | —                                                        | —                                                              | Lifts an **indefinite** exclusion only, and only after its 90‑day floor (`responsible.go:73`). Fixed periods expire on their own. `204`.                                                                                                                                                                                                                                                                                                                                                                                                                                                                      |
| GET    | `/v1.0/wallet/gambling/limits`              | `wallet.go:211` | —                                                        | —                                                              | Returns `{limits, usage, excluded}` — current caps, São‑Paulo window sums + reset times, exclusion state. `responsible.go:191`.                                                                                                                                                                                                                                                                                                                                                                                                                                                                               |
| PUT    | `/v1.0/wallet/gambling/limits`              | `wallet.go:221` | —                                                        | `{daily,weekly,monthly:int64>0}`                               | Sets the three personal deposit caps (must satisfy `0 < daily ≤ weekly ≤ monthly`). Decreases apply immediately; increases wait out a cooldown (7d, or 14d when monthly grows) as a `pending` set. First configuration / activation requires limits. Audited. `responsible.go:102`.                                                                                                                                                                                                                                                                                                                           |
| DELETE | `/v1.0/wallet/gambling/limits/pending`      | `wallet.go:237` | —                                                        | —                                                              | Cancels a scheduled increase (keeps stricter current limits). `204`.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                          |
| POST   | `/v1.0/wallet/gambling/activate`            | `wallet.go:95`  | `RequireKYC(basic)` (`router.go:76`)                     | `{accept_addendum:bool(required), daily,weekly,monthly:int64}` | Opts the caller into `game`+`sandbox`. Records gambling‑addendum acceptance (audited) then activates. Gates: `kyc_level != ""` (a **minimum** of `basic`, so `enhanced` also passes — see `ActivateGambling`) **and** current gambling addendum accepted. Mandatory limits on first activation. Idempotent (replay returns same wallets, appends nothing). `wallet.go:128`.                                                                                                                                                                                                                                                                                                              |
| POST   | `/v1.0/wallet/game/deposit`                 | `wallet.go:115` | `RequireKYC(verified)`                                   | `{amount:int64>0}`                                             | **Registered ONLY when `GAMBLING_ENABLED=true`** (`router.go:73`) — else `404`. `real→game` (`FundGame`): the **one** edge real money enters the ring‑fence, metered by the personal limit engine (GROSS INFLOW, Invariant #8). Also capped at `MaxInboundAmount`. `wallet.go:680`.                                                                                                                                                                                                                                                                                                                           |

### 3b. Custody onboarding

There is one deposit rail — a PIX static QR on the user's own Asaas subaccount,
opened under their CPF — so onboarding is a precondition for depositing at all,
not an optional upgrade. The provider charges a **non-refundable** fee per
subaccount at creation, which is why paying it and opening the subaccount are
two separate durable steps. Design: `../docs/specs/2026-08-30-asaas-only-deposits.md`.

Statuses: `fee_pending` → `fee_paid` → `onboarding` → `pending_documents` →
`pending_approval` → `approved`. A provider **rejection returns to
`pending_documents`**, never to `closed`: the fee is already spent, so the user
re-sends documents rather than paying again.

| Method | Path                        | Scope                     | Handler       | Body                              | Behaviour |
|--------|-----------------------------|---------------------------|---------------|-----------------------------------|-----------|
| POST   | `/v1.0/wallet/onboarding`   | `wallet:custody:write` + `RequireKYC(enhanced)` | `baas.go:33` | `{income_value:int64>0}` | Reserves the caller's custody record and opens the verification-fee charge. Creates **no** subaccount — the provider bills the moment one exists. Refused with `404` for a user outside the `custody_enabled` allowlist (operational state, not a feature). Idempotent: called again while the fee is outstanding it returns the same stored QR; called after onboarding moved on it returns the status with no charge. Returns `{status, fee?{amount, qr_code, qr_code_base64, refundable:false}, onboarding_url?}`. |
| GET    | `/v1.0/wallet/onboarding`   | `wallet:balances:read`    | `baas.go:56`  | —                                 | The onboarding step the UI polls. Read-only: opens no charge and no subaccount. For an in-progress account older than the provider's 15s minimum it first re-queries `GET /v3/myAccount/status` and the pending-document list, so the response names the actual outstanding step (and its `onboarding_url`) instead of a generic "under review". `404` before onboarding starts. |
| POST   | `/v1.0/wallet/closure`      | `wallet:custody:write` + `RequireRecentMFA(5m)` | `baas.go:17` | — | Starts the account-closure state machine (plan §7.2). `202 Accepted` with the new status. |

### 3c. Asaas webhook routes (static token, no JWT)

Gated on the `asaas-access-token` header compared in constant time against
`/{service}/{env}/asaas/webhook-token` (`middleware/asaas_webhook.go`), which
**fails closed on an empty configured token**. These are ordinary API routes:
the mTLS-verified HTTP API in `cdk/lib/pix-gateway-stack.ts` exists because
Inter demands a client certificate, and Asaas offers a shared token instead.

| Method | Path                                            | Handler        | Behaviour |
|--------|-------------------------------------------------|----------------|-----------|
| POST   | `/v1.0/internal/asaas/webhook`                  | `baas.go:121`  | Single entry point for every asynchronous Asaas event. `ACCOUNT_STATUS_*` → re-query `GET /v3/myAccount/status` and act only on `general == APPROVED` (a `500` is returned on a failed re-query so Asaas redelivers). `PAYMENT_RECEIVED`/`PAYMENT_CONFIRMED` → split by external-reference prefix: `vfee#` is the verification fee (settled on CTech's master account, creates the subaccount), anything else is a user deposit. `TRANSFER_MED_CLAWBACK`/`PIX_MED_RETURNED` stay **quarantined with an ALARM** — no authoritative provider-side MED query exists yet, and a bearer-token webhook must never debit a wallet on its own. |
| POST   | `/v1.0/internal/asaas/transfer-authorization`   | `asaas.go:88`  | Synchronous approve/refuse for every outbound transfer. Compares amount **and** destination against the `wallet_transfer_intents` row written before submission; any divergence, missing field, or unknown reference is `REFUSED` with a reason. Always `200` — the verdict is in the body. |

---

## 4. Internal M2M routes (Bearer M2M + scope)

Scope constants: `middleware/scope.go:11`. Every route also rejects a
non‑empty `sid` (`scope.go:42`).

| Method | Path                                               | Scope (const)                                     | Handler           | Body                                                                        | Behaviour                                                                                                                                                                                                                                                      |
|--------|----------------------------------------------------|---------------------------------------------------|-------------------|-----------------------------------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| POST   | `/v1.0/internal/pix/confirm-deposit`               | `internal:wallet:confirm-deposit` (`scope.go:15`) | `internal.go:12`  | `{txid, payer_cpf?, payer_name?}` (`dto.go:69`)                             | Called by **pix‑gateway's webhook Lambda** (which already re‑queried Inter). api **re‑queries Inter itself again** via the Lambda PixClient before crediting (Invariant #11). CPF match anti‑fraud gate; amount must equal the opened charge. `wallet.go:278`. |
| POST   | `/v1.0/internal/pix/confirm-product-purchase`      | `internal:wallet:confirm-deposit` (`scope.go:15`) | `m2m_product_purchase.go` | `{txid}` (`ConfirmPurchaseRequest`) | Called by pix-gateway for reserved `prdp` txids. Re-queries Inter, validates completed status and the catalog-owned amount, then conditionally transitions `wallet_product_purchases` from `pending` to `confirmed` and notifies the owning M2M client. Never touches a wallet balance or ledger. |
| POST   | `/v1.0/internal/wallet/sandbox/credit`             | `internal:wallet:credit` (`scope.go:12`)          | `internal.go:24`  | `{user_id, amount>0, idempotency_key, reason?}` (`dto.go:52`)               | Grants sandbox currency (e.g. poker/dominó bonus). `CreditSandbox`.                                                                                                                                                                                            |
| POST   | `/v1.0/internal/wallet/sandbox/debit`              | `internal:wallet:debit` (`scope.go:13`)           | `internal.go:37`  | same                                                                        | Spends sandbox currency (e.g. a bet). `DebitSandbox`. **Sandbox‑only** — distinct from `real` debit.                                                                                                                                                           |
| POST   | `/v1.0/internal/wallet/real/debit`                 | `internal:wallet:debit-real` (`scope.go:14`)      | `internal.go:52`  | same                                                                        | Debits the **real** wallet for an authorized M2M client (e.g. `ctech-billing` subscription). No PIX leg. **Deliberately separate from `internal:wallet:debit`** so a sandbox‑only client can never touch `real`. `wallet.go:811`.                              |
| POST   | `/v1.0/internal/wallet/game/hold`                  | `internal:wallet:game-hold` (`scope.go:22`)       | `internal.go:66`  | `{user_id, amount>0, table_ref, idempotency_key}` (`dto.go:78`)             | Reserves a buy‑in against `game` (real conditional debit, Invariant #1). Hold record never bounds the later cash‑out. `wallet.go:843`.                                                                                                                         |
| POST   | `/v1.0/internal/wallet/game/hold/:hold_id/release` | `internal:wallet:game-hold`                       | `internal.go:80`  | `{user_id, idempotency_key}` (`dto.go:89`)                                  | Refunds a `held` hold in full (table/hand aborted before play). Requires `user_id` to match the hold's owner (SEC‑07). Idempotent. `wallet.go:871`.                                                                                                            |
| POST   | `/v1.0/internal/wallet/game/cashout`               | `internal:wallet:game-cashout` (`scope.go:24`)    | `internal.go:95`  | `{user_id, amount>0, table_ref, hold_ids[], idempotency_key}` (`dto.go:97`) | Fail-closed settlement: amount may not exceed the referenced held value. Wallet credit, ledger/idempotency guard, and every `held→settled` transition commit atomically. Multi-player winnings require the future zero-sum settlement protocol.                |
| GET    | `/v1.0/internal/wallet/game/status/:user_id`       | `internal:wallet:game-status` (`scope.go:27`)     | `internal.go:111` | —                                                                           | Real‑money eligibility for a skill game: `{activated, self_excluded, limits_configured}`. Registered unconditionally so poker sees "not eligible" even while the flag is off. `responsible.go:236`.                                                            |
| GET    | `/v1.0/internal/wallet/balance/:user_id`           | `internal:wallet:balance` (`scope.go:32`)         | `internal.go:121` | —                                                                           | Read‑only `{game_balance, sandbox_balance}` (centavos). `real` deliberately excluded — poker never touches real money directly. Never creates a wallet; a wallet that doesn't exist reports `0`. `wallet.go` `BalancesFor`.                                    |

> **Scope naming note (divergence B2/B3).** The scope guarding
> `/internal/pix/confirm-deposit` is `internal:wallet:confirm-deposit`
> (pix‑gateway → api). The wallet's **own** M2M client requests
> `internal:account:kyc` from `ctech-account` to read the verified CPF
> (`kycclient/kycclient.go:24`) — a *different* scope on a *different*
> service. Some older docs/comments conflate the two; the code is
> authoritative. Similarly `internal:wallet:debit-real` (code, `scope.go:14`)
> is the correct real‑wallet debit scope; the stale string
> `internal:wallet:real:debit` survives only in
> `docs/specs/2026-07-19-poker-game-holds-design.md`.

---

## 5. Money math (all integer **centavos**)

- **Withdrawal** — debits exactly the requested amount. CTech does not charge a
  withdrawal fee and writes no fee ledger entry.
- **PIX deposit range** — per‑wallet `min_deposit`/`max_deposit` override
  defaults `100`/`1000000` (R$1 / R$10.000) (`deposit_limits.go:9`); minimum
  never below absolute floor `100`; incoherent `max<min` widens rather than
  rejecting. Checked before `CreateCharge`.
- **Absolute inbound ceiling** `MaxInboundAmount = 1_000_000 × 100` (R$1.000.000)
  — hard cap on a single deposit **or** `real→game` fund; no override may
  exceed it (`model.go:105`).
- **Sandbox conversion** `SandboxCreditsPerCent = 10` (R$1 ⇒ 1000 credits)
  (`model.go:115`).
- `real↔game` transfers carry **no fee**.

---

## 6. Financial Safety Invariants — how each is enforced in code

1. **Balance never negative** — every debit carries `balance >= :amount`
   condition in `balanceTx` (`repositories/wallet.go:591`, expr at `:596`).
   Failure ⇒ `insufficient-balance` (`problem.go:143`).
2. **Ledger append‑only** — `ledger_entries` written only via
   `BuildPutTxItemIfAbsent`; authoritative balance is `wallets.balance`
   (`model.go:149`). `wallet_audit` likewise append‑only
   (`repositories/audit.go:28`).
3. **Idempotent** — guard item `IDEM#{key}` written `attribute_not_exists` in
   the same `TransactWriteItems` (`repositories/wallet.go:619`); replay by
   `Idempotency-Key` header (user) or `idempotency_key` body (internal)
   returns the prior result, and a payload hash drift ⇒ `idempotency-conflict`.
   Guard rows are permanent; a replay does not become fresh after a retention window.
   (`checkReplay:638`).
4. **One op / wallet** — Valkey `SETNX` lock, `LockTTL = 10s` auto‑release
   (`lock/lock.go:23`). Contention ⇒ `wallet-busy` (`problem.go:147`).
5. **Cross‑wallet lock order** — `lock.AcquireOrdered` sorts keys
   lexicographically (total, deadlock‑free); used by `ringTransfer`
   (`wallet.go:655`).
6. **Sandbox never becomes real** — `PurchaseSandbox` debits `game`, never
   `real`; no route accepts `type=sandbox` for withdrawal/conversion
   (`wallet.go:746`).
7. **Real money enters ring‑fence ONLY via `real→game`** — `FundGame` is the
   single `real→game` edge (`wallet.go:680`); regression test
   `TestSandboxPurchaseNeverDebitsRealWallet` guards it.
8. **`real→game` limit = GROSS INFLOW** — `FundGame` meters and **never**
   refunds headroom on `ReturnFromGame` (`wallet.go:737`).
9. **`game` is real money** — withdrawable via `real`; total real = `real +
   game` (`model.go:6`).
10. **Consent opt‑in + auditable** — `game` absent until KYC at least `basic` +
    current gambling addendum accepted; every activation / limit change
    appends to `wallet_audit` (`Event*` in `domain/wallet/audit.go:6`). `sandbox`
    is play currency and is created independently, lazily, with no KYC/consent
    requirement (`EnsureSandboxWallet`, `repositories/wallet.go`).
11. **Webhook never source of truth** — `ConfirmDeposit` re‑queries Inter by
    `txid` before crediting; webhook body only supplies the payer CPF/name
    (masked‑compare, fails closed) (`wallet.go:278`, `maskedCPFMatches:392`).
12. **No money in limbo** — withdrawal `processing` resolved by the
    **reconcile** job (`services/reconcile.go:33`): completed ⇒ mark done,
    not‑found ⇒ reverse (credit back), failed reversal ⇒ `refund_failed` +
    alarm. Deposit sweep re‑queries pending deposits, but a paid deposit with no
    verified payer identity remains quarantined and is never credited.
    (`reconcile.go:112`). Stale `held` holds alarm only, never auto‑release
    (`reconcile.go:135`).

---

## 7. Known divergences (documented, NOT fixed here)

| ID  | Where                                                                                                                                                                                                                                                                                                                                                              | Status                      |
|-----|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-----------------------------|
| B1  | Every money op uses `Base.TransactWrite` → `dynamodb:TransactWriteItems` (`repositories/wallet.go:275,323,383,444`, `base.go` wraps `ctech-go-common/dynamo`). Verify the CDK IAM role grants `dynamodb:TransactWriteItems` — if missing, **all** money ops are denied at runtime.                                                                                 | Open — see `cdk/README.md`. |
| B2  | `internal:account:kyc` (wallet→account KYC, `kycclient.go:24`) vs `internal:wallet:confirm-deposit` (pix‑gateway→api) — distinct scopes; misleading comments at `kycclient.go:2` and `config.go:41`.                                                                                                                                                               | Documented.                 |
| B3  | `internal:wallet:debit-real` (code, `scope.go:14`) vs stale `internal:wallet:real:debit` in `docs/specs/2026-07-19-poker-game-holds-design.md`.                                                                                                                                                                                                                    | Documented.                 |
| B7  | Valkey fail‑closed in prod (`config.go:74`) but `newCacheBackend` silently falls back to in‑memory on missing/!redis (`app.go:65`) and even on redis connect failure (`app.go:72`); same for the WS registry (`app.go:91`). Non‑prod ⇒ locks/WS not fleet‑shared with no hard failure.                                                                             | Open.                       |
| B18 | Money constants mirrored api↔ui (no float): `SANDBOX_CREDITS_PER_CENTAVO=10` (`ui/src/lib/utils/money.ts:46` ↔ `model.go:115`). No fee constants exist on either side any more (`docs/specs/2026-08-16-withdrawal-fee-removal.md`). **`rpc-contract` defines NO money constants** — the mirror is purely api↔ui. Keep them in sync by hand. | Documented. |
