# CLAUDE.md — api (ctech-wallet-api)

Go REST API — Fiber v3, DynamoDB, Valkey, PIX (Banco Inter **via `pix-gateway`**), AWS SDK v2.

**Before any task:** Read `../docs/specs/2026-07-10-wallet-design.md` and the root `../CLAUDE.md` Financial Safety
Invariants. This service custodies real money — those invariants override convenience.

> The API never talks to Inter directly: every PIX op invokes `pix-gateway`'s
> outbound Lambda (`PIX_GATEWAY_FUNCTION_NAME`) with a bearer from
> `InterTokenManager`. The Inter mTLS cert/secret live in `pix-gateway`/SSM.
> Endpoint contract: **[ENDPOINTS.md](ENDPOINTS.md)**.

---

## Role

Custodies three balances per user (real + game + sandbox), an append-only ledger, PIX deposit/withdraw via
`pix-gateway` (which fronts Inter), and sandbox M2M credit/debit for integrated apps. Bridges the frontend and the
Inter partner bank; consumes auth + KYC from ctech-account.

**Request flow:** `HTTP → Middleware (auth → scope/KYC/step-up) → Route → Service → Repository → DynamoDB`

Not multi-tenant: no organization header, no RBAC. Access control is user JWT + M2M scopes + step-up MFA.

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
│   ├── pix/                    # PixClient iface + fake + Lambda client (talks to pix-gateway, not Inter)
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
  (R$1/R$10.000); the minimum never drops below the absolute 100-centavo floor. Checked *before* `CreateCharge`.
- Deposit-range fields are admin-only (edited directly in DynamoDB) — never a client/API write path.
- `real ↔ game` transfers carry no fee in either direction.
- Every balance mutation is a conditional `TransactWriteItems`; debits carry `balance >= :amount`.
- The ledger (`ledger_entries`) is append-only — never updated or deleted; the authoritative balance is
  `wallets.balance`, never derived from the ledger. `wallet_audit` is append-only for the same reason and holds
  the non-money events (consent, activation, limit changes). IAM DENIES `UpdateItem`/`DeleteItem` on both.
- Every mutation is idempotent via a guard item `IDEM#{key}` (`attribute_not_exists`) co-written in the same
  transaction.
- One op per wallet at a time via the Valkey lock; cross-wallet ops go through `lock.AcquireOrdered`, which sorts
  wallet IDs (`real` → `game` → `sandbox`) so the order is total and deadlock-free.

### The gambling ring-fence (CRITICAL)

- `game` holds **real money**, ring-fenced for games. `sandbox` is virtual and is a **sink** — nothing converts it
  back.
- Real money reaches a game or sandbox **only** across `real → game` (`FundGame`). Sandbox is bought from `game`,
  never from `real`. That one edge is where personal limits are enforced; a second door makes them meaningless.
- `game → real` (`ReturnFromGame`) is never limited and never charged.
- `game` does not exist until `ActivateGambling` (verified KYC + gambling addendum). Every `game`-touching
  operation goes through `requireActivated`. `sandbox` does **not** share this gate — it is play currency and
  is created lazily on first M2M sandbox credit/debit (`EnsureSandboxWallet`), independent of KYC/consent.
- The whole surface is gated by `GAMBLING_ENABLED` (default **false**) — the routes are not registered when it is
  off. Do not turn it on before the personal limit engine ships.

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
5. Cross‑wallet ops lock in order `real → game → sandbox` (`lock.AcquireOrdered`).
6. Sandbox never becomes real (sink).
7. Real money enters the ring‑fence ONLY via `real → game`.
8. `real → game` limit counts GROSS INFLOW (returns never refund headroom).
9. `game` is real money (withdrawable via `real`; total = `real + game`).
10. Consent opt‑in + auditable (`wallet_audit` append‑only).
11. PIX webhook never source of truth — re‑query Inter by `txid` before crediting.
12. No money in limbo — `processing` withdrawals resolved by the reconcile job.

## Internal M2M scopes (constant table: `middleware/scope.go:11`)

| Scope                              | Guards                                                                    | Notes                                                                                                                         |
|------------------------------------|---------------------------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------|
| `internal:wallet:credit`           | `POST /internal/wallet/sandbox/credit`                                    | sandbox only                                                                                                                  |
| `internal:wallet:debit`            | `POST /internal/wallet/sandbox/debit`                                     | sandbox only                                                                                                                  |
| `internal:wallet:debit-real`       | `POST /internal/wallet/real/debit`                                        | **real** wallet — distinct from `:debit`                                                                                      |
| `internal:wallet:confirm-deposit`  | `POST /internal/pix/confirm-deposit`, `/confirm-sandbox-purchase`, `/confirm-product-purchase` | requested by **pix‑gateway**; prefix-routed wake-ups, API always re-queries Inter                                              |
| `internal:wallet:game-hold`        | `POST .../game/hold`, `.../hold/:id/release`                              |                                                                                                                               |
| `internal:wallet:game-cashout`     | `POST .../game/cashout`                                                   |                                                                                                                               |
| `internal:wallet:game-status`      | `GET .../game/status/:user_id`                                            |                                                                                                                               |
| `internal:wallet:balance`          | `GET .../wallet/balance/:user_id`                                         | read-only, game+sandbox only                                                                                                  |
| `internal:wallet:sandbox-purchase` | `POST .../wallet/sandbox-purchase/`, `GET .../:id`, `POST .../:id/refund` | M2M-opened direct PIX→sandbox sale (e.g. ctech-poker); see `docs/specs/2026-07-30-m2m-sandbox-purchase-integration-design.md` |

The wallet's **own** M2M client requests `internal:account:kyc` from
`ctech-account` to read the verified CPF (`kycclient/kycclient.go:24`) — a
different scope on a different service. Do not conflate it with
`internal:wallet:confirm-deposit`.

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
