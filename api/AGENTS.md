# CLAUDE.md — api (ctech-wallet-api)

Go REST API — Fiber v3, DynamoDB, Valkey, AWS SDK v2. Pix moves through **ctech-billing** (it owns the Inter connection).

Family standard (layering, DI, errors, resilience, logging, CI gate): the `ctech-dev-standards` skill, `references/backend.md`.
This file holds only what is wallet-specific.

**Before any task:** Read `docs/specs/2026-10-14-wallet-ledger-billing-rail.md` in the `ctech-billing` repo (what the wallet is
now) and the root `../CLAUDE.md` Financial Safety Invariants. This service custodies real money — those invariants override convenience.

> The API never talks to Inter: deposits are Billing invoices and withdrawals are Billing payouts, through the `rail`
> package (`rail.BillingRail`, `billingclient`). The old Pix gateway, its Lambdas and the Inter secrets are gone.
> Endpoint contract: **[ENDPOINTS.md](ENDPOINTS.md)**.

---

## Role

Custodies two balances per user (`real` + `sandbox`) and an append-only ledger. User deposits and withdrawals go through
Billing with daily limits (`../docs/specs/2026-10-08-wallet-restoration-design.md`); Sandbox credit/debit and real
debit/holds are M2M operations for integrated apps (Poker). It sells nothing and knows no gambling rule: Poker owns
consent, limits, self-exclusion and the SKUs. Consumes auth + KYC from ctech-account.

**Request flow:** `HTTP → Middleware (auth → scope/KYC/step-up) → Route → Service → Repository → DynamoDB`

Not multi-tenant: no organization header, no RBAC. Access control is user JWT + M2M scopes (step-up MFA
middleware guards `POST /wallet/withdrawals`). Layering is handler → service → repository with fx DI (see the skill).

---

## Directory Structure

```
api/
├── cmd/server/main.go          # slog JSON + fx.New(app.Module).Run()
├── cmd/reconcile/main.go       # withdrawal reconciliation job
├── internal/
│   ├── config/                 # 12-Factor env config (caarlos0/env)
│   ├── walletproblem/          # wallet problem types and constructors on api-commons/problem
│   ├── validation/             # go-playground/validator singleton
│   ├── awsclient/              # aws-sdk-go-v2 (DynamoDB only)
│   ├── lock/                   # per-wallet lock (wraps api-commons/lock; `wallet:` key prefix)
│   ├── billingclient/          # ctech-billing HTTP client (invoices, payouts, customers)
│   ├── oauthresource/          # RFC 9728 metadata + scope-manifest.json
│   ├── secrets/                # SSM reader
│   ├── middleware/             # auth (JWKS), user scope, KYC (guards: go-common authguard)
│   ├── rail/                   # Rail iface, BillingRail (the only rail), types and a Fake for tests
│   ├── kycclient/              # ctech-account internal KYC client
│   ├── domain/wallet/          # models, constants, deposit/daily limits, erasure
│   ├── domain/id/              # ULID generation
│   ├── repositories/           # persistence only — DynamoDB access
│   ├── services/               # business logic (incl. reconcile, erasure)
│   ├── app/                    # fx module wiring
│   └── api/v1/                 # routes + helpers
└── tests/integration/          # //go:build integration — DynamoDB-local
```

---

## Engineering Rules

Generic rules (workflow, DRY, named constants, RFC 7807 via `sendProblem`/`problem.*`, layer separation, fx DI, no
goroutines in handlers, RS256 only, zero-warning lint, tests with the change, no secrets) are in the skill. Wallet
specifics: route errors go through `sendProblem(c, err)`; the wallet problem types are in `internal/walletproblem`
(`InsufficientBalance`, `WalletBusy`, `WithdrawCPFMismatch`, `KYCNotVerified`, `IdempotencyConflict`, `StepUpRequired`,
`DailyDepositLimit`, `DailyWithdrawLimit`, `AccountErasurePending`, `RouteRetired`, ...). The deployed binary is named `app`.
Never commit Billing/Inter credentials, webhook secrets, JWT keys, AWS credentials or real CPFs.

### Outbound calls (Billing, KYC)

`billingclient` and `kycclient` use `resilient.NewHTTPClient` (breaker per host, up to 3 attempts, jitter backoff).
Retried: GETs and any request with an `Idempotency-Key` (every Billing mutation but `OpenPix`; the key is the
deterministic wallet reference, so a replay is deduped by Billing) on a transport error or 5xx. A 4xx is never
retried: 408/425/429 are no longer retried (the old client did not retry at all; the family loop retried them).
Both fail CLOSED: an error or non-2xx is returned, a KYC lookup failure is never "not found", and there is no
negative cache (a stale 404 on a payout lookup would reverse money that was paid). One call, retries included, is
bounded by the client timeout (Billing 8s, KYC 10s).

### Money & ledger (CRITICAL)

- All amounts are integer **centavos**. Never float.
- Deposit range is per-wallet: optional `min_deposit`/`max_deposit` override the defaults (R$1/R$10.000); the minimum
  never drops below the absolute 100-centavo floor. Checked *before* any charge is opened at the provider.
- Daily PIX limits per wallet (`daily_deposit_cap` R$1.000, `daily_withdraw_cap` R$1.000, `daily_withdraw_count` 1):
  `domain/wallet/daily_limits.go`; counters live on the user row (`real_daily_counters`), written under the real
  wallet lock in the same transaction as the money movement. Deposit cap enforced at credit (excess refunded).
- Deposit-range and daily-limit fields are admin-only (edited directly in DynamoDB) — never a client/API write path.
- Every balance mutation is a conditional `TransactWriteItems`; debits carry `balance >= :amount`.
- The ledger (`ledger_entries`) is append-only — never updated or deleted; the authoritative balance is
  `wallets.balance`, never derived from the ledger. `wallet_audit` is append-only for the same reason and holds
  the non-money events. IAM DENIES `UpdateItem`/`DeleteItem` on both.
- Every mutation is idempotent via a guard item `IDEM#{key}` (`attribute_not_exists`) co-written in the same
  transaction.
- One op per wallet at a time via the Valkey lock; cross-wallet ops go through `lock.AcquireOrdered`, which sorts
  wallet IDs so the order is total and deadlock-free (independent of wallet type).

- **Account-deletion lock:** every NEW service method that opens exposure for a user (debit, credit, hold, charge,
  purchase, deposit, consent or limit write) starts with `checkErasure(ctx, s.erasureGate, userID)`, and its test joins
  `TestErasureGateRefusesEveryNewExposure`. Settlement of what already exists (confirm, release, cash-out, sweeps)
  never calls it: that money cannot be refused, so it surfaces as an erasure blocker instead.

### Real and Sandbox never mix (CRITICAL)

- `sandbox` is virtual and is a **sink**: nothing converts it back, and nothing converts `real` into it. It is bought from
  Poker with a Pix paid through Billing; the wallet only credits and debits it on an M2M call.
- The list of routes that write is **closed** (`TestNoRouteConvertsSandboxToAnything`); a new one fails the test on
  purpose. Retired routes answer `410 route-retired` (after the normal 401), never a 404.
- Holds, cash-outs and `BalancesFor` act on `real` (the game wallet was merged into it; old `game` rows are retired,
  read-only data). A hold is refused while a self-exclusion recorded here is active (fail closed); consent, limits and
  exclusion are otherwise Poker's, checked before it calls the wallet.

## Testing

| Change             | Required                                    |
|--------------------|---------------------------------------------|
| Ledger / balance   | Unit + integration (no-negative, atomic)    |
| Idempotency        | Unit + integration (replay = same result)   |
| Lock / concurrency | Integration (concurrent op → `wallet-busy`) |
| Deposit flow       | Integration (Billing event → re-read → credit) |
| Bug fix            | Reproduce + regression                      |

Run: `make test` (unit, `-race`) and `make test-integration` (needs `docker compose -f docker-compose.test.yml up -d`).

---

## Critical Areas (require analysis before touching)

- Ledger credit/debit transaction shape and the idempotency guard
- Per-wallet locking and cross-wallet lock ordering
- Deposit confirmation (Billing event → re-read invoice → credit/refund) and the CPF gate
- Withdrawal `processing` state and the reconciliation job
- JWT validation, scope, KYC, and step-up middleware

---

## Financial Safety Invariants (summary — full text in root `../CLAUDE.md`)

These are the reason the service exists; a change weakening any is wrong. All are
enforced in code — see [ENDPOINTS.md §6](ENDPOINTS.md#6-financial-safety-invariants--how-each-is-enforced-in-code)
for the `file:line` map.

1. Balance never negative — debit `ConditionExpression: balance >= :amount`.
2. Ledger append‑only — authoritative balance is `wallets.balance`.
3. Every mutation idempotent — `IDEM#{key}` guard co‑written in the same txn.
4. One op/wallet at a time — Valkey `SETNX` + TTL.
5. Cross‑wallet ops lock in one total order (`lock.AcquireOrdered`).
6. Sandbox never becomes real, real never becomes Sandbox.
7–10. *(Retired 2026-10-15: the ring-fence rules, the `game` wallet and consent/limits left with the game wallet; see the root `CLAUDE.md`.)*
11. A Billing event never source of truth — re‑read the invoice from Billing before crediting.
12. Custody on CTech's Inter account is an explicit accepted risk for a closed KYC-approved group; a deposit credits only
    when the payer matches the KYC CPF **and** Billing says `payer_match = match` (else quarantined and refunded),
    payouts go only to the KYC CPF
    (`../docs/specs/2026-10-08-wallet-restoration-design.md`).
13. *(Retired 2026-10-07 — was the BaaS verification-fee rule.)*
14. No money in limbo — `processing` withdrawals resolved by the reconcile job from Billing; a failed lookup never reverses.

## Internal M2M scopes (constant table: `middleware/scope.go`)

| Scope                              | Guards                                                                    | Notes                                                                                                                         |
|------------------------------------|---------------------------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------|
| `internal:wallet:credit`           | `POST /internal/wallet/sandbox/credit`                                    | sandbox only                                                                                                                  |
| `internal:wallet:debit`            | `POST /internal/wallet/sandbox/debit`                                     | sandbox only                                                                                                                  |
| `internal:wallet:debit-real`       | `POST /internal/wallet/real/debit`                                        | **real** wallet — distinct from `:debit`                                                                                      |
| `internal:wallet:hold`             | `POST .../wallet/holds/`, `.../holds/cashout`, `.../holds/:id/release`    | Poker stakes `real` through these three; the old game-named routes answer 410                                                 |
| `internal:wallet:balance`          | `GET .../wallet/balance/:user_id`                                         | read-only: `real` (as what a game can stake) and sandbox                                                                      |
| `internal:wallet:erasure-eligibility` | `GET /internal/erasure/eligibility/:sub`                               | account-deletion saga (token minted by ctech-account)                                                                         |

The wallet's **own** M2M client requests `internal:account:kyc` from
`ctech-account` to read the verified CPF (`kycclient/kycclient.go`) — a
different scope on a different service. Do not conflate it with
`internal:wallet:hold`.

## Notes and known gaps

- Money constants shared with the UI (`SANDBOX_CREDITS_PER_CENTAVO` = 100, `MAX_AMOUNT_CENTS`) live in
  `../rpc-contract/money.json`; Go (`rpc-contract/money_test.go`) and UI (`money-contract.test.mjs`) tests pin them. No fee
  constants exist (`docs/specs/2026-08-16-withdrawal-fee-removal.md`).
- Production fails closed on an empty `VALKEY_URL`, `SERVICE_AUDIENCE`, `CTECH_ISSUER_URL`, CORS, `ERASURE_QUEUE_URL` or
  `VALKEY_REVOCATION_URL` (`internal/config/config.go`).
- Gaps vs the standard (code changes, tracked in the root `CLAUDE.md`): local `internal/middleware`; `alerts` covers refund/reversal
  failures only (the other `ALARM` lines stay log-only). Per-user rate limits live in `internal/api/v1/ratelimit.go` (reads 120/min FailOpen, money 10/min FailClosed).

---

## Completion Checklist

- [ ] `go build ./...` compiles; `make test` passes
- [ ] Integration tests pass (`make test-integration`)
- [ ] Financial Safety Invariants upheld
- [ ] Cross-project impact reviewed (api ↔ ui ↔ cdk ↔ ctech-account)
