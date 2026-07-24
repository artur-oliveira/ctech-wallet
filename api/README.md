# ctech-wallet API

Go REST API (Fiber v3) for the `aoctech.app` digital wallet. Custodies **real
third‑party money** across three balances per user (`real`, `game`, `sandbox`)
with an append‑only ledger, PIX (Banco Inter) deposits/withdrawals, and
skill‑game holds/cash‑outs. Talks to Inter **only through `pix-gateway`** (a
Lambda) — it never opens an mTLS connection itself.

> **This service custodies real money.** The 12 Financial Safety Invariants in
> the repo root `CLAUDE.md` are non‑negotiable and override convenience.

## Layout

```
api/
├── cmd/server/main.go        # fx.New(app.Module).Run() — the HTTP API
├── cmd/reconcile/main.go     # scheduled Lambda / CLI: resolves stuck withdrawals
├── internal/
│   ├── app/                  # fx wiring (DI), Fiber app, error handler
│   ├── config/               # 12-Factor env (caarlos0/env)
│   ├── problem/              # RFC 7807 Problem + wallet codes
│   ├── validation/           # go-playground/validator singleton
│   ├── awsclient/            # aws-sdk-go-v2 (DynamoDB, Lambda)
│   ├── lock/                 # Valkey SETNX per-wallet lock (fail-safe TTL)
│   ├── middleware/           # auth (JWKS), scope, KYC, step-up
│   ├── pix/                  # PixClient iface + fake + Lambda client + Inter token
│   ├── kycclient/            # ctech-account internal KYC client
│   ├── domain/wallet/        # models, constants, fee/limit/responsible math
│   ├── domain/id/            # ULID generation
│   ├── repositories/         # DynamoDB persistence (single-table helpers)
│   ├── services/             # business logic (the money path)
│   └── api/v1/               # routes, DTOs, helpers, WS
└── tests/integration/        # //go:build integration — DynamoDB-local
```

Request flow: `HTTP → Middleware (auth → scope/KYC/step-up) → Route → Service
→ Repository → DynamoDB`. Not multi‑tenant (no org header, no RBAC).

## Build & run

```bash
make build                 # linux/arm64 static binary named `app` → dist/app
make test                  # unit (race)
make test-integration      # needs DynamoDB Local: docker compose -f docker-compose.test.yml up -d
make reconcile             # build the reconciliation binary
```

Dockerfile is distroless (`golang:1.26-alpine` → `distroless/static-debian12`),
binary at `/app`, `EXPOSE 8000`. The deployed binary **must** be named `app`
(`Makefile:1`, CDK userdata expects `/opt/app/current/app`).

### Environment (`config.Load`, `.env.example`)

Required: `TABLE_PREFIX`, `PIX_GATEWAY_FUNCTION_NAME`. Set in prod:
`SERVICE_AUDIENCE`, `CTECH_URL`, `VALKEY_URL` (fail‑closed if absent — see B7),
`WALLET_CLIENT_ID`/`WALLET_CLIENT_SECRET` (wallet's own M2M client to call
account KYC). `GAMBLING_ENABLED` (default `false`) gates the entire
`real→game` funding + activation surface (routes 404 when off).

> **Drift note:** `.env.example` still lists `INTER_*` vars
> (`INTER_BASE_URL`, `INTER_CLIENT_ID`, `INTER_CLIENT_SECRET`, `INTER_PIX_KEY`,
> `INTER_WEBHOOK_SECRET`). The **api** no longer consumes these — it invokes
> `pix-gateway` via `PIX_GATEWAY_FUNCTION_NAME` and the Inter mTLS/secret live in
> `pix-gateway`/SSM. Those `INTER_*` lines belong to `pix-gateway`, not this
> service; treat them as stale in this file.

## Data model (single DynamoDB table per concern, env‑prefixed)

| Table (logical) | Constant | Notes |
|-----------------|----------|-------|
| `wallets` | `TableWallets` (`model.go:74`) | authoritative balance (centavos for real/game, credits for sandbox) |
| `wallet_ledger_entries` | `TableLedger` (`:75`) | append‑only audit; GSI `gsi_idem` for replay |
| `wallet_idempotency` | `TableIdempotency` (`:76`) | `IDEM#{key}` guard rows (TTL 7d) |
| `wallet_pix_deposits` | `TablePixDeposits` (`:77`) | pending charges; GSI `gsi_status` for sweep |
| `wallet_withdrawals` | `TableWithdrawals` (`:78`) | `processing`/`completed`/`reversed`/`refund_failed`; GSI `gsi_status` |
| `wallet_users` | `TableUsers` (`:79`) | consent + responsible‑gambling state |
| `wallet_audit` | `TableAudit` (`:80`) | append‑only non‑money events |
| `wallet_holds` | `TableHolds` (`:81`) | game buy‑in holds; GSI `gsi_hold_status` |

Every balance mutation is a conditional `TransactWriteItems`
(`balance >= :amount` on debits); the ledger entry + idempotency guard are
co‑written in the same transaction (`repositories/wallet.go:275`).

## Endpoint reference

See **[ENDPOINTS.md](ENDPOINTS.md)** — all routes, methods, auth/scope, request
& response shapes, business rules, side‑effects, and the invariant‑by‑invariant
enforcement map.

## Cross‑links

- Repo root: [`../CLAUDE.md`](../CLAUDE.md) (Financial Safety Invariants),
  [`../README.md`](../README.md), [`../OPERATIONS.md`](../OPERATIONS.md)
- Sibling services: [`../ui/README.md`](../ui/README.md),
  [`../cdk/README.md`](../cdk/README.md),
  [`../pix-gateway/README.md`](../pix-gateway/README.md),
  [`../rpc-contract/README.md`](../rpc-contract/README.md)
- Shared wire contract: [`../rpc-contract/types.go`](../rpc-contract/types.go)
- Design specs: `../docs/specs/2026-07-10-wallet-design.md`,
  `../docs/specs/2026-07-12-three-wallet-topology-design.md`,
  `../docs/specs/2026-07-19-responsible-gambling-design.md`

## Known divergences

Tracked in [ENDPOINTS.md §7](ENDPOINTS.md#7-known-divergences-documented-not-fixed-here)
(B1 IAM `TransactWriteItems`, B2/B3 scope strings, B7 Valkey fail‑closed,
B18 money‑constant mirror). See also root `CLAUDE.md` "Open divergences".
