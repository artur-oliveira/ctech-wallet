# CLAUDE.md — api (ctech-wallet-api)

Go REST API — Fiber v3, DynamoDB, Valkey, PIX (Inter), AWS SDK v2.

**Before any task:** Read `../docs/specs/2026-07-10-wallet-design.md` and the root `../CLAUDE.md` Financial Safety
Invariants. This service custodies real money — those invariants override convenience.

---

## Role

Custodies three balances per user (real + game + sandbox), an append-only ledger, PIX deposit/withdraw via Inter, and
sandbox M2M credit/debit for integrated apps. Bridges the frontend and the Inter partner bank; consumes auth +
KYC from ctech-account.

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
│   ├── pix/                    # PixClient interface + fake + Inter (mTLS)
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
- Withdrawal fee is **per-wallet**: optional `fee_bps`/`fee_min`/`fee_max` on the `wallets` row override the
  defaults (2%/R$1/R$10); the result never drops below the absolute 100-centavo floor.
- PIX deposit range is per-wallet the same way: optional `min_deposit`/`max_deposit` override the defaults
  (R$1/R$10.000); the minimum never drops below the absolute 100-centavo floor. Checked *before* `CreateCharge`.
- Fee and deposit-range fields are admin-only (edited directly in DynamoDB) — never a client/API write path.
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
- `game`/`sandbox` do not exist until `ActivateGambling` (verified KYC + gambling addendum). Every ring-fence
  operation goes through `requireActivated`.
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

## Completion Checklist

- [ ] `go build ./...` compiles; `make test` passes
- [ ] Integration tests pass (`make test-integration`)
- [ ] No duplication introduced (searched before creating)
- [ ] All constants named (no magic strings/numbers)
- [ ] Errors returned via `sendProblem` / `problem.*` helpers
- [ ] Financial Safety Invariants upheld
- [ ] Cross-project impact reviewed (api ↔ ui ↔ cdk ↔ ctech-account)
