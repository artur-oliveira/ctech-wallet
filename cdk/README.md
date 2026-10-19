# ctech-wallet CDK

AWS CDK (TypeScript, `@aoctech/cdk` 0.10.2) infrastructure for the wallet. Family rules: `ctech-dev-standards`
skill (`references/infra.md`). Deploys: DynamoDB tables, the account-deletion queue, the API on an EC2 ASG behind the shared
HAProxy edge, the reconcile Lambda and the GitHub-Actions OIDC roles.

Not here: the frontend (Cloudflare Workers Static Assets, deployed by `.github/workflows/frontend.yml` through the reusable
workflow in `ctech-cdk`; there is no S3 or CloudFront) and any pix-gateway (removed; `ctech-billing` owns the Inter connection).

Entry: `bin/ctech-wallet-cdk.ts` (`cdk.json` -> `npx ts-node --prefer-ts-exts bin/ctech-wallet-cdk.ts`). Per environment;
shared infra (VPC, edge SG, Valkey) is owned by `ctech-cdk` and referenced via SSM.

## Stacks

| Stack | File | Provisions |
|-------|------|-----------|
| `CtechWallet-Global-OIDC` | `lib/oidc-stack.ts` | GitHub Actions roles via OIDC (API deploy, reconcile deploy, infra with `AdministratorAccess`, scope publisher) |
| `DynamoDBStack` | `lib/dynamodb-stack.ts` | 12 live tables + GSIs, plus 4 retired BaaS tables kept as `RETAIN` |
| `ErasureStack` | `lib/erasure-stack.ts` | account-deletion queue (visibility 5 min, retention 14 d) + DLQ (`maxReceiveCount` 5), subscribed to account's SNS topic with filter `services=wallet` |
| `IAMStack` | `lib/iam-stack.ts` | EC2 instance role for the API |
| `ApiStack` | `lib/api-stack.ts` | EC2 ASG + nginx + deploy scripts (`HaproxyEc2Service`) |
| `ReconcileStack` | `lib/reconcile-stack.ts` | reconcile Lambda + EventBridge Scheduler every 5 min |

## DynamoDB (`dynamodb-stack.ts`)

Names are env-prefixed (`TABLE_PREFIX` = env, so `dev_wallets`). OnDemand with `maxRead/WriteRequestUnits: 1000`; PITR **prod
only**; encryption AWS-managed key; `RETAIN` outside dev, `DESTROY` in dev. All GSIs project `ALL`.

| Logical table | GSI(s) | Notes |
|---------------|--------|-------|
| `wallets` | `gsi_user` | authoritative balance |
| `wallet_ledger_entries` | `gsi_idem` | **append-only** |
| `wallet_idempotency` | none | permanent `IDEM#{key}` guards |
| `wallet_pix_deposits` | `gsi_status`, `gsi_user` | keyed by txid; **no table TTL** (expiry closes the charge, never deletes the record) |
| `wallet_withdrawals` | `gsi_status`, `gsi_user` | `processing` rows drive reconcile |
| `wallet_users` | none | per-user state; TTL on carried self-exclusions |
| `wallet_holds` | `gsi_hold_status`, `gsi_user` | game buy-in holds on `real` |
| `wallet_audit` | none | **append-only** (the purge may only remove `ip`/`user_agent`) |
| `wallet_sandbox_purchases`, `wallet_product_purchases` | status, webhook-status, `gsi_user` | old purchase records (read-only history) |
| `wallet_erasure_state` | none | account-deletion lock/tombstone, TTL |
| `wallet_erasure_retention` | none | write-only retained PII (API role may only `PutItem`), TTL 5 years |

Retired and kept only so CloudFormation does not delete them: `wallet_baas_accounts`, `wallet_transfer_intents`,
`wallet_settlement_legs`, `wallet_med_receivables` (forced `RETAIN`, no IAM access). Names/keys/GSIs mirror
`api/internal/domain/wallet/model.go`; a mismatch silently breaks every query.

## IAM

Mutable tables: `GetItem, PutItem, UpdateItem, DeleteItem, Query, BatchGetItem, ConditionCheckItem, DescribeTable`. No `Scan`.
Append-only tables (`wallet_ledger_entries`, `wallet_audit`) get create + read, an explicit DENY on `DeleteItem`, DENY on
`UpdateItem` for the ledger, and for the audit table `UpdateItem` only when it touches `pk`/`sk`/`ip`/`user_agent` (LGPD
anonymization). `dynamodb:TransactWriteItems` is not an IAM action: a transaction needs the item-level actions above, which are
granted (the old "B1" finding was a false positive).

## ApiStack (`api-stack.ts`)

- `HaproxyEc2Service` owns the SG, launch template, log groups, ASG and CPU target tracking. `ctech-lbalancer` discovers the
  instances through its `wallet` route and probes `HEALTH_CHECK_PATH` (`/v1.0/health`, dependency-free), not
  `/v1.0/health-check`.
- Instances: `t4g.nano` On-Demand, min 1 / max 2 in every environment (`onDemand: true`; no Spot). Alpine arm64 by default
  (`OS_FAMILY=al2023` is the rollback). nginx `:8080` -> app `:8000` (a second app process on `:8001` for the in-place
  rolling restart), rate limit 100 r/s by real viewer IP.
- User data writes nginx, systemd/OpenRC service, `start.sh` (non-secret env from SSM at deploy time, secrets from SSM at each
  start: `VALKEY_URL` DB **2**, `VALKEY_REVOCATION_URL` = base Valkey DB 0, account URLs, audience/CORS, Billing and
  wallet client secrets), `deploy.sh`, log upload, logrotate.
- **No CloudWatch alarms and no custom metrics.** The CloudWatch agent is logs-only. `ALARM` lines (refund/reversal failure,
  quarantined deposit, stale hold) land in `/ctech-wallet/<env>/app` and are found with Logs Insights; nothing pages anyone and
  there is no SNS alert from the wallet yet.
- The SSM agent is **on** (`ENABLE_SSM_AGENT = true` in `bin/ctech-wallet-cdk.ts`): CI deploys over SSM RunCommand.
- The daytime-only schedule (`enableCron`/`disableCron`) is commented out in `api-stack.ts`: the ASG runs continuously in every
  environment.

### How the API is deployed

`.github/workflows/api.yml` builds `app`, uploads the zip to the deployments bucket (`current.zip` is what new instances
bootstrap), then runs `ctech-cdk`'s `deploy-backend` action: an SSM RunCommand of `/opt/app/deploy.sh` on every in-service
instance. `deploy.sh` restarts the app and the second app process one at a time behind nginx, gating each on its health check,
so a single instance keeps serving during a code deploy. No ASG instance refresh is started by this repo's workflows, and no
`MinHealthyPercentage` is set anywhere in this repo. When a CDK change replaces the launch template, CloudFormation applies the
construct's rolling update policy (`minInstancesInService = min(minCapacity, maxCapacity - 1)` = 1 here, from `@aoctech/cdk`
0.10.2). With max 2 there is one instance of headroom, so a launch-template replacement is the one place capacity can dip;
there is no WebSocket drain step before termination.

## ReconcileStack (`reconcile-stack.ts`)

Lambda (`provided.al2023`, arm64, 256 MB, 5 min timeout), **not** in the VPC (in-memory locker; needs only DynamoDB, Billing and
account over public endpoints). EventBridge Scheduler every **5 min** (`RECONCILE_RATE_MINUTES`); keep it well below the API's
`sweepAgeThreshold` (10m) and `depositTTLMinutes` (60m). The role covers `wallets`, ledger, idempotency, withdrawals, sandbox and
product purchases, deposits (read/update/query) and holds (query only), plus the SSM parameters for Billing and the wallet
client. It uses `config.LoadReconcile`, so API-only startup guards do not block this non-HTTP job.

## OidcStack (`oidc-stack.ts`)

One-time global stack: GitHub Actions roles via OIDC, no long-lived keys. Roles: API deploy, reconcile deploy, infra
(`AdministratorAccess`) and `ctech-wallet-gha-scopes` (reads only Account's app URL and the wallet's bound publisher client
credentials). The trust subject is `repo:<repo>:*`.

## Deploy flow

CI: `.github/workflows/deploy.yml` (infra -> scopes -> API -> frontend), calling `infra.yml`, `api.yml` and `frontend.yml`.
Manual, per environment:

```bash
cd ../../ctech-cdk
CTECH_AWS_PROFILE=ctech ./scripts/configure-service-url-parameters.sh <env>
cd ../ctech-wallet/cdk
npm ci
npx cdk deploy CtechWallet-<Env>-DynamoDB      # then IAM, Erasure, API, Reconcile
```

The API resolves account transport/JWKS and wallet audience/CORS URLs from SSM at every service start, so changing them needs
an SSM change plus a service restart, not a template change. Internal transport uses `*.internal.aoctech.app`; the OIDC issuer
and browser URLs stay public. The reconcile Lambda stays on public endpoints because it runs outside the shared VPC.

## Cross-links

- API: [`../api/README.md`](../api/README.md)
- Secrets / SSM seeding: [`../OPERATIONS.md`](../OPERATIONS.md)
- Money constants: [`../rpc-contract/README.md`](../rpc-contract/README.md)
