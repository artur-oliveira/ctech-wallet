# CLAUDE.md — api (ctech-wallet-api)

Go REST API — Fiber v3, DynamoDB, Valkey, AWS SDK v2. Pix moves through **ctech-billing** (it owns the Inter connection).

**Before any task:** Read `docs/specs/2026-10-14-wallet-ledger-billing-rail.md` in `ctech-billing` (what the wallet is
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
middleware guards `POST /wallet/withdrawals`).

---

## Directory Structure

```
api/
├── cmd/server/main.go          # slog JSON + fx.New(app.Module).Run()
├── cmd/reconcile/main.go       # withdrawal reconciliation job
├── internal/
│   ├── config/                 # 12-Factor env config (caarlos0/env)
│   ├── problem/                # RFC 7807 Problem type + wallet codes
│   ├── validation/             # go-playground/validator singleton
│   ├── cache/                  # Redis/Valkey + in-memory backends
│   ├── awsclient/              # aws-sdk-go-v2 (DynamoDB only)
│   ├── lock/                   # Valkey SETNX per-wallet lock
│   ├── middleware/             # auth (JWKS), scope, KYC, step-up
│   ├── rail/                   # Rail iface, BillingRail (the only rail), types and a Fake for tests
│   ├── kycclient/              # ctech-account internal KYC client
│   ├── domain/wallet/          # models, constants, fee calc
│   ├── domain/id/              # ULID generation
│   ├── repositories/           # persistence only — DynamoDB access
│   ├── services/               # business logic
│   └── api/v1/                 # routes + helpers
└── tests/integration/          # //go:build integration — DynamoDB-local
```

---

## Mandatory Workflow

1. Read the spec + root Financial Safety Invariants before starting.
2. `rg "..."` — search for existing implementations before creating new code.
3. Plan → Implement (TDD for ledger/idempotency/locking) → Run affected tests.
4. Update the spec/docs for new endpoints/schemas/scopes.
5. State which components were cross-reviewed (api ↔ ui ↔ cdk ↔ ctech-account).
6. Suggest a Conventional Commit (no emojis, no `Co-Authored-By`).

---

## Engineering Rules

### DRY

Never duplicate functions. Search `internal/` before adding any function, type, or constant. Money math, attribute
names, scope strings, and cache-key prefixes are defined once.

### Constants — no magic strings/numbers

All string keys, status codes, table-name suffixes, header names, cache-key prefixes, scopes, and ledger entry
types MUST be named constants. The `Idempotency-Key` header and scope strings are defined once.

### Error Handling (MUST follow)

- All route errors go through `sendProblem(c, err)` — never raw errors, `fiber.Map`, or `fiber.NewError`.
- Services return `*problem.Problem` via the `problem.*` helpers (incl. wallet codes `InsufficientBalance`,
  `WalletBusy`, `WithdrawCPFMismatch`, `KYCNotVerified`, `IdempotencyConflict`, `StepUpRequired`).

### Layer Separation (strictly enforced)

| Layer      | Allowed                                         | Forbidden                            |
|------------|-------------------------------------------------|--------------------------------------|
| Repository | DynamoDB read/write only                        | Business logic, cache, HTTP concerns |
| Service    | Business logic, cache/lock, PIX, KYC calls      | DynamoDB SDK calls, HTTP parsing     |
| Route      | Parse request, call ONE service method, respond | Business logic, repo calls           |

### Dependency Injection

Services, repositories, PIX/KYC clients, and AWS clients are injected via `go.uber.org/fx`. Never instantiate
them inside route handlers.

### Money & ledger (CRITICAL)

- All amounts are integer **centavos**. Never float.
- PIX deposit range is per-wallet the same way: optional `min_deposit`/`max_deposit` override the defaults
  (R$1/R$10.000); the minimum never drops below the absolute 100-centavo floor. Checked *before* any charge is
  opened at the provider.
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
  wallet IDs (`real` → `sandbox`) so the order is total and deadlock-free.

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

### Go Rules

- No goroutines inside request handlers — Fiber handles concurrency (reconciliation runs in its own process).
- `aws-sdk-go-v2` only. Auth is RS256-only. No `SECRET_KEY`, no HS256.
- Binary deployed to EC2 must be named `app`.

### Secrets

Never commit: Inter mTLS certs/client secret, webhook secret, JWT keys, AWS credentials, real CPFs.

---

## Testing

| Change             | Required                                    |
|--------------------|---------------------------------------------|
| Fee calculation    | Unit (min/max boundaries)                   |
| Ledger / balance   | Unit + integration (no-negative, atomic)    |
| Idempotency        | Unit + integration (replay = same result)   |
| Lock / concurrency | Integration (concurrent op → `wallet-busy`) |
| PIX flow           | Integration (webhook → re-query → credit)   |
| Bug fix            | Reproduce + regression                      |

Run: `make test` (unit) and `make test-integration` (needs `docker compose -f docker-compose.test.yml up -d`).

---

## Critical Areas (require analysis before touching)

- Ledger credit/debit transaction shape and the idempotency guard
- Per-wallet locking and cross-wallet lock ordering
- PIX deposit confirmation (webhook → re-query → credit/refund) and the CPF gate
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
5. Cross‑wallet ops lock in order `real → sandbox` (`lock.AcquireOrdered`).
6. Sandbox never becomes real, real never becomes Sandbox.
7–10. *(Retired 2026-10-15: the ring-fence rules, the `game` wallet and consent/limits left with the game wallet; see the root `CLAUDE.md`.)*
11. A Billing event never source of truth — re‑read the invoice from Billing before crediting.
12. Custody on CTech's Inter account is an explicit accepted risk for a closed KYC-approved group; a deposit credits only
    when the payer matches the KYC CPF **and** Billing says `payer_match = match` (else quarantined and refunded),
    payouts go only to the KYC CPF
    (`../docs/specs/2026-10-08-wallet-restoration-design.md`).
13. *(Retired 2026-10-07 — was the BaaS verification-fee rule.)*
14. No money in limbo — `processing` withdrawals resolved by the reconcile job from Billing; a failed lookup never reverses.

## Internal M2M scopes (constant table: `middleware/scope.go:11`)

| Scope                              | Guards                                                                    | Notes                                                                                                                         |
|------------------------------------|---------------------------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------|
| `internal:wallet:credit`           | `POST /internal/wallet/sandbox/credit`                                    | sandbox only                                                                                                                  |
| `internal:wallet:debit`            | `POST /internal/wallet/sandbox/debit`                                     | sandbox only                                                                                                                  |
| `internal:wallet:debit-real`       | `POST /internal/wallet/real/debit`                                        | **real** wallet — distinct from `:debit`                                                                                      |
| `internal:wallet:hold`             | `POST .../wallet/holds/`, `.../holds/cashout`, `.../holds/:id/release`    | Poker stakes `real` through these three; the old game-named routes answer 410                                                 |
| `internal:wallet:balance`          | `GET .../wallet/balance/:user_id`                                         | read-only: `real` (as what a game can stake) and sandbox                                                                      |

The wallet's **own** M2M client requests `internal:account:kyc` from
`ctech-account` to read the verified CPF (`kycclient/kycclient.go:24`) — a
different scope on a different service. Do not conflate it with
`internal:wallet:hold`.

## Known divergences (documented, NOT fixed here)

| ID  | Summary                                                                                                                                                                                                    | Anchor                                                           |
|-----|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|------------------------------------------------------------------|
| B1  | IAM may be missing `dynamodb:TransactWriteItems` — every money op uses `Base.TransactWrite` (→ `ctech-go-common/dynamo`). If absent, all money ops denied at runtime.                                      | `repositories/wallet.go:275`; verify `cdk/README.md`             |
| B2  | `internal:account:kyc` (wallet→account) vs `internal:wallet:confirm-deposit` (pix‑gateway→api) conflated in some comments (`kycclient.go:2`, `config.go:41`).                                              | `kycclient.go:24`, `scope.go:15`                                 |
| B3  | `internal:wallet:debit-real` (code) vs stale `internal:wallet:real:debit` in `docs/specs/2026-07-19-poker-game-holds-design.md`.                                                                           | `scope.go:14`                                                    |
| B7  | **Fixed:** prod fails closed on empty `VALKEY_URL` (`config.go:74`) AND on Valkey init failure (`app.go` `newCacheBackend` returns error in prod). Non‑prod still falls back to in‑memory with a warn log. | `app.go:65`                                                      |
| B18 | Money constants mirrored api↔ui by hand (no float): `SANDBOX_CREDITS_PER_CENTAVO=10`. No fee constants on either side — see `docs/specs/2026-08-16-withdrawal-fee-removal.md`. `rpc-contract` holds NO money constants. | `model.go:115`, `ui/src/lib/utils/money.ts` |

---

## Completion Checklist

- [ ] `go build ./...` compiles; `make test` passes
- [ ] Integration tests pass (`make test-integration`)
- [ ] No duplication introduced (searched before creating)
- [ ] All constants named (no magic strings/numbers)
- [ ] Errors returned via `sendProblem` / `problem.*` helpers
- [ ] Financial Safety Invariants upheld
- [ ] Cross-project impact reviewed (api ↔ ui ↔ cdk ↔ ctech-account)

## Mandatory Documentation Policy

**Every code change MUST be documented.**

There are NO exceptions.

Any modification affecting behavior, architecture, APIs, integrations, configuration, deployment, security, business
rules, or developer workflow MUST include the corresponding documentation update in the same change.
