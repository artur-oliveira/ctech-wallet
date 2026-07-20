# CLAUDE.md — cdk (ctech-wallet-cdk)

AWS CDK (TypeScript) infrastructure for the wallet. Deploys the DynamoDB
tables, the API on an EC2 ASG behind an ALB, the reconcile Lambda, the
`pix-gateway` Lambdas, the static frontend (S3 + CloudFront), and the
GitHub-Actions OIDC deploy roles.

**Before any task:** Read the root [`../CLAUDE.md`](../CLAUDE.md) Financial
Safety Invariants. This stack custodies real money — the invariants are
enforced here wherever they can be (IAM, table shape), and the rest in `api`.

> **B1 is a FALSE POSITIVE (closed).** `dynamodb:TransactWriteItems` is
> not an IAM action — a `TransactWriteItems` call needs the item‑level
> permissions (`ConditionCheckItem`/`DeleteItem`/`PutItem`/`UpdateItem`),
> which the wallet IAM role already grants. Monetary transactions work in
> production. No IAM change needed. (See `README.md`.)

---

## Stacks (`bin/ctech-wallet-cdk.ts`, `cdk.json` → ts-node)

| Stack | File | Provisions |
|-------|------|-----------|
| `DynamoDBStack` | `lib/dynamodb-stack.ts` | 8 tables + GSIs (OnDemand) |
| `IAMStack` | `lib/iam-stack.ts` | EC2 instance role for the API |
| `ApiStack` | `lib/api-stack.ts` | EC2 ASG + ALB + nginx + CloudWatch alarm |
| `ReconcileStack` | `lib/reconcile-stack.ts` | reconcile Lambda + EventBridge Scheduler (5 min) |
| `PixGatewayStack` | `lib/pix-gateway-stack.ts` | outbound + webhook Lambdas, mTLS HTTP API |
| `FrontendStack` | `lib/frontend-stack.ts` | S3 + CloudFront + URL-rewrite Function + KVS route store |
| `OidcStack` | `lib/oidc-stack.ts` | GitHub Actions deploy roles (OIDC, no keys) |

Shared infra (VPC, ALB, Valkey) is owned by `ctech-cdk` and referenced
via SSM (`lib/constants.ts` `SSM_SHARED`).

## DynamoDB (`lib/dynamodb-stack.ts`)

All tables env-prefixed (`TABLE_PREFIX=env` ⇒ `dev_wallets`). **OnDemand**
(`maxRead/WriteRequestUnits: 1000`); PITR enabled **prod only**
(`:70-71`); encryption `awsManagedKey`; `RETAIN` prod / `DESTROY` dev.

| Logical table | GSI(s) | Key notes |
|---------------|--------|-----------|
| `wallets` | `gsi_user` | pk `WALLET#{id}`; authoritative balance |
| `wallet_ledger_entries` | `gsi_idem` | pk+sk; **append-only** (Invariant #2) |
| `wallet_idempotency` | — | `IDEM#{key}` guards; **TTL** 7d |
| `wallet_pix_deposits` | `gsi_status` | keyed by txid; **TTL** 60m |
| `wallet_withdrawals` | `gsi_status` | `processing`/`completed`/… drives reconcile |
| `wallet_users` | — | consent + responsible-gambling state |
| `wallet_holds` | `gsi_hold_status` | game buy-in holds |
| `wallet_audit` | — | pk+sk; **append-only** (Invariant #10) |

Names/keys/GSIs mirror `api/internal/domain/wallet/model.go` exactly
(`dynamodb-stack.ts:8-11`) — a mismatch silently breaks every query.

## How the Financial Safety Invariants are enforced in infra

- **#2 / #10 (append-only ledger & audit):** `APPEND_ONLY_TABLES`
  (`constants.ts:178`) → IAM grants create+read and an explicit **DENY** on
  `UpdateItem`/`DeleteItem` (`iam-stack.ts:97-111`). A bug or a compromised
  instance can add but never rewrite/erase the record.
- **#4 / #5 (one op/wallet, lock order):** enforced in `api` via Valkey
  `SETNX` (`VALKEY_DB=2`, `constants.ts:112`), **not** in CDK. The ASG
  shares one Valkey (shared VPC) so locks are fleet-wide.
- **#1 / #3 / #6-#9 / #11 / #12:** enforced in `api` code (see
  `../api/ENDPOINTS.md` §6). CDK only provisions the tables the code needs.
- **#12 (no money in limbo):** `ReconcileStack` EventBridge every 5 min
  (`RECONCILE_RATE_MINUTES`, `reconcile-stack.ts`); role touches `wallets,
  wallet_ledger_entries, wallet_idempotency, wallet_withdrawals` (+indexes)
  and invokes pix-gateway's outbound Lambda.

## Engineering Rules

### Constants — no magic strings
Every resource name, listener priority, SSM path, port, and table/GSIs name
lives in `lib/constants.ts` / `lib/types.ts` (root CLAUDE.md: "Constants —
no magic variables"). Names the CDK creates are never inlined at a call site.

### IAM — least privilege
- No `dynamodb:Scan` anywhere — GetItem/Query only.
- Append-only tables get a NARROWER action set + explicit DENY (Invariant #2/#10).
- Inter mTLS/OAuth/webhook secrets live in **pix-gateway's** role
  (`pix-gateway-stack.ts`), never the API role — the API no longer talks to
  Inter (`iam-stack.ts:115-137` scopes SSM to `wallet-client-id/secret`,
  `ctech-account/*`, `ctech/{env}/*`).
- **B1 is a false positive (closed)** — `TransactWriteItems` is not an IAM action; the item‑level perms it needs are already granted. No IAM change.

### Money
All money math is **integer centavos**, computed in `api` — CDK stores
exactly what `api` writes; never round or transform values here.

## Critical Areas (analyze before touching)

- The IAM role — grants the item‑level actions (`PutItem`/`UpdateItem`/`ConditionCheckItem`/`DeleteItem`) that `TransactWriteItems` needs (B1 was a false positive).
- Table names/keys/GSIs — must match `api/internal/domain/wallet/model.go`.
- The reconcile Lambda role + scheduler cadence (must stay below the API's
  `sweepAgeThreshold` 10m and `depositTTLMinutes` 60m).

## Deploy flow

```bash
cd cdk && npm ci
npx cdk deploy CtechWallet-<Env>-DynamoDB   # then IAM, Api, Reconcile, PixGateway, Frontend
```
CI: `.github/workflows/{api,frontend,infra,deploy}.yml`.

## Known divergences (documented, NOT fixed here)

| ID | Where | Status |
|----|-------|--------|
| **B1** | `dynamodb:TransactWriteItems` absent from API (`iam-stack.ts:81`) + reconcile roles. **Runtime-blocking.** | Open — fix before real money. |
| — | OPERATIONS.md §4 registers webhook as `…/webhook?hmac=` but CDK routes `POST /pix/webhook` (`pix-gateway-stack.ts`). Path mismatch. | Doc gap. |
| — | OPERATIONS.md §4 omits the pix-gateway webhook M2M secret `/ctech-wallet/{env}/pix-gateway/client-secret`. | Doc gap — seed it. |

## Cross-links

- API: [`../api/README.md`](../api/README.md) · pix-gateway: [`../pix-gateway/README.md`](../pix-gateway/README.md)
- Secrets / SSM seeding: [`../OPERATIONS.md`](../OPERATIONS.md)
- Wire contract: [`../rpc-contract/README.md`](../rpc-contract/README.md)
