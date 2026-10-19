# CLAUDE.md — cdk (ctech-wallet-cdk)

Family infra standard (CDK frozen, no NAT/ALB/CloudWatch alarms/custom metrics, alerts via the shared SNS topic, deploy
rules): the `ctech-dev-standards` skill, `references/infra.md`. This file holds only what is wallet-specific.

AWS CDK (TypeScript, `@aoctech/cdk` 0.10.2) for the wallet: DynamoDB tables, the account-deletion queue, the API on an EC2 ASG
behind the shared HAProxy edge, the reconcile Lambda and the GitHub-Actions OIDC roles. The frontend is **not** here: it
deploys to Cloudflare Workers Static Assets through `.github/workflows/frontend.yml` (reusable workflow in `ctech-cdk`).
There is no pix-gateway: Billing owns the Inter connection.

**Before any task:** Read the root [`../CLAUDE.md`](../CLAUDE.md) Financial Safety Invariants. This stack custodies real money;
the invariants are enforced here wherever they can be (IAM, table shape), the rest in `api`. Details: [`README.md`](README.md).

## Stacks (`bin/ctech-wallet-cdk.ts`)

| Stack | File | Provisions |
|-------|------|-----------|
| `CtechWallet-Global-OIDC` | `lib/oidc-stack.ts` | GitHub OIDC roles: API deploy, reconcile deploy, infra (AdministratorAccess), scope publisher |
| `DynamoDBStack` | `lib/dynamodb-stack.ts` | 12 live tables + 4 retired BaaS tables kept as RETAIN (OnDemand) |
| `ErasureStack` | `lib/erasure-stack.ts` | account-deletion SQS queue + DLQ, SNS subscription (`services=wallet`) |
| `IAMStack` | `lib/iam-stack.ts` | EC2 instance role for the API |
| `ApiStack` | `lib/api-stack.ts` | EC2 ASG (`HaproxyEc2Service`) + nginx + deploy scripts |
| `ReconcileStack` | `lib/reconcile-stack.ts` | reconcile Lambda + EventBridge Scheduler (5 min) |

Shared infra (VPC, edge SG, Valkey) is owned by `ctech-cdk` and referenced via SSM (`lib/constants.ts` `SSM_SHARED`).

## How the Financial Safety Invariants are enforced in infra

- **#2 (append-only ledger and audit):** `APPEND_ONLY_TABLES` (`constants.ts`) get create+read only and an explicit DENY on
  `DeleteItem`; `UpdateItem` is denied outright on the ledger, and on `wallet_audit` is allowed only to remove `ip`/`user_agent`
  (LGPD anonymization, `AUDIT_ANONYMIZABLE_ATTRIBUTES`). A bug or a compromised instance can add but not rewrite the record.
- **#4 / #5 (one op per wallet, lock order):** enforced in `api` via Valkey `SETNX` (`VALKEY_DB=2`), not in CDK.
- **Others:** enforced in `api` code (see `../api/ENDPOINTS.md` §6). CDK only provisions the tables the code needs.
- **#14 (no money in limbo):** `ReconcileStack` runs every 5 min (`RECONCILE_RATE_MINUTES`); it must stay well below the API's
  `sweepAgeThreshold` (10m) and `depositTTLMinutes` (60m). Its role covers `wallets`, ledger, idempotency, withdrawals,
  sandbox/product purchases, deposits (read/update/query) and holds (query only).

## Rules specific to this repo

- Table names, keys and GSIs mirror `api/internal/domain/wallet/model.go` exactly; a mismatch silently breaks every query.
- No `dynamodb:Scan` in any role. Never recreate a table: logical ids are frozen (the retired BaaS tables stay declared with
  `RETAIN` and no IAM until a reviewed change removes them).
- The API role has no Inter/Billing secrets of its own beyond the SSM paths it reads at boot (`iam-stack.ts`).
- `TransactWriteItems` is not an IAM action; the item-level actions it needs are granted (the old "B1" finding was a false positive).

## Deploy

CI is `.github/workflows/deploy.yml` (infra -> scopes -> API -> frontend; calls `infra.yml`, `api.yml`, `frontend.yml`).
Manual: `cd cdk && npm ci && npx cdk deploy CtechWallet-<Env>-DynamoDB` then IAM, Erasure, API, Reconcile.
The API deploy is an SSM RunCommand of `/opt/app/deploy.sh` per in-service instance, not an ASG instance refresh; see README.

## Known gaps vs the standard (code/infra, not docs)

- DynamoDB GSIs use `ProjectionType.ALL` (standard prefers `KEYS_ONLY`/`INCLUDE`).
- OIDC trust is `repo:<repo>:*` (any ref), not an explicit `allowedSubSuffixes`.
- `bin/ctech-wallet-cdk.ts` hard-codes a VPC id fallback; `lib/constants.ts` still has dead ALB/CloudFront constants
  (`ALB_LISTENER_PRIORITY`, `API_PATH_PATTERNS`, `CERT_ARN`, `LOCALE_*`, `frontendBucketName`, `routeStoreName`).
- `.github/workflows/frontend-cloudfront.yml` exists but `deploy.yml` calls `frontend.yml` (Cloudflare).
- No SNS alert wiring: reconcile/ALARM failures are log lines.
