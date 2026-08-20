# ctech-wallet CDK

> HAProxy migration: the API ASG no longer creates an ALB target group or listener
> rule. `ctech-lbalancer` discovers it through its `wallet` route; the retained
> `/ctech/{env}/network/alb-sg-id` identifies the shared edge trusted by the API SG.
> `HaproxyEc2Service` from `@aoctech/cdk` now owns the common ASG resources.
> Route creation remains disabled because the existing `wallet` route parameter is
> owned by `ctech-lbalancer`.

AWS CDK (TypeScript) infrastructure for the wallet. Deploys: DynamoDB tables,
the API on an EC2 ASG behind the CTech HAProxy edge, the reconcile Lambda, the `pix-gateway`
Lambdas, the static frontend (S3 + CloudFront), and the GitHub‑Actions OIDC
deploy roles.

Entry: `bin/ctech-wallet-cdk.ts` (`cdk.json` → `npx ts-node --prefer-ts-exts
bin/ctech-wallet-cdk.ts`). Per‑environment; shared infra (VPC, edge SG, Valkey) is
owned by `ctech-cdk` and referenced via SSM.

## Stacks

| Stack | File | Provisions |
|-------|------|-----------|
| `DynamoDBStack` | `lib/dynamodb-stack.ts` | 8 tables + GSIs (OnDemand) |
| `IAMStack` | `lib/iam-stack.ts` | EC2 instance role for the API |
| `ApiStack` | `lib/api-stack.ts` | EC2 ASG + HAProxy route + nginx + deploy scripts |
| `ReconcileStack` | `lib/reconcile-stack.ts` | reconcile Lambda + EventBridge Scheduler (5 min) |
| `PixGatewayStack` | `lib/pix-gateway-stack.ts` | outbound + webhook Lambdas, mTLS HTTP API |
| `FrontendStack` | `lib/frontend-stack.ts` | S3 + CloudFront + URL‑rewrite Function + KVS route store |
| `OidcStack` | `lib/oidc-stack.ts` | GitHub Actions deploy roles (OIDC, no keys) |

## DynamoDB (`dynamodb-stack.ts`)

All tables env‑prefixed (`TABLE_PREFIX=env` ⇒ `dev_wallets`). **OnDemand** with
`maxRead/WriteRequestUnits: 1000`; PITR enabled **prod only**; encryption
`awsManagedKey`; `RETAIN` prod / `DESTROY` dev.

| Logical table | GSI(s) | Key notes |
|---------------|--------|-----------|
| `wallets` | `gsi_user` (user_id) | pk `WALLET#{id}`; authoritative balance |
| `wallet_ledger_entries` | `gsi_idem` (idempotency_key) | pk+sk; **append‑only** |
| `wallet_idempotency` | — | Permanent `IDEM#{key}` guards; financial replay protection never expires |
| `wallet_pix_deposits` | `gsi_status` (status) | keyed by txid; **TTL** 60m |
| `wallet_withdrawals` | `gsi_status` (status) | `processing`/`completed`/… drives reconcile |
| `wallet_users` | — | consent + responsible‑gambling state |
| `wallet_holds` | `gsi_hold_status` (status) | game buy‑in holds |
| `wallet_audit` | — | pk+sk; **append‑only** (consent/limits) |

Names/keys/GSIs mirror `api/internal/domain/wallet/model.go` exactly
(`dynamodb-stack.ts:9`) — a mismatch silently breaks every query.

## IAM — CRITICAL divergence **B1** (documented, NOT fixed here)

The API instance role (`iam-stack.ts:81-93`) grants on **mutable** tables:
`dynamodb:GetItem, PutItem, UpdateItem, Query, BatchGetItem,
ConditionCheckItem, DescribeTable`. Append‑only tables (`wallet_ledger_entries`,
`wallet_audit`) get a **narrower** set plus an explicit `DENY` on
`UpdateItem`/`DeleteItem` (`:107`). The reconcile role (`reconcile-stack.ts:93`)
mirrors this.

**B1 is a FALSE POSITIVE (closed).** `dynamodb:TransactWriteItems` is
**not** an IAM action — a `TransactWriteItems` call needs the *item‑level*
permissions (`ConditionCheckItem`, `DeleteItem`, `PutItem`, `UpdateItem`),
which the wallet IAM role **already grants**. Monetary transactions
**work in production** (confirmed by the operator). The IAM comment at
`iam-stack.ts:88` asserts the mutations are conditional `TransactWriteItems`,
and they are — the underlying item actions are present. No IAM change needed.

## ApiStack — EC2 ASG + HAProxy (`api-stack.ts`)

- Uses `HaproxyEc2Service` for the private-IPv4 security group, encrypted launch
  template, log groups, ASG and CPU target tracking. The shared HAProxy edge discovers
  the ASG through its existing `wallet` route and probes `/v1.0/health-check`; the API's
  degraded `207` response remains part of that route's health contract.
- Instances: min 1, max 3 (prod). nginx `:8080` → app `:8000`
  (`constants.ts:44-48`). Rate limit `100r/s` by real viewer IP
  (`limit_req_zone`, `:182`). WebSocket `/v1.0/ws` upgrade proxied
  (`:223`). Real‑IP resolved via `update-realip.sh`.
- **User data** (`api-stack.ts:96`) writes nginx.conf, systemd `app.service`,
  `start.sh` (fetches non‑secret env + reads secrets from SSM at boot:
  `VALKEY_URL` DB **2**, internal `CTECH_URL`/`CTECH_JWKS_URL`, public issuer,
  service audience/CORS, `WALLET_CLIENT_ID`/
  `SECRET`), `deploy.sh` (kept for local/manual use — CI no longer calls it),
  `upload-logs.sh`, logrotate.
- User data downloads only the official Cloudflare Origin CA RSA root, verifies
  its pinned SHA-256 and installs it into the system trust store for verified
  `*.internal.aoctech.app` calls.
- **No CloudWatch alarms and no custom metrics** (2026-08-19). The CloudWatch
  agent config is logs-only — no `metrics` block, no `CtechWallet/<env>/Host`
  namespace — because EC2 already publishes `CPUUtilization`/`CPUCreditBalance`
  for free and nothing alarmed on the host/process series. The `"ALARM"` log-line
  alarm (refund/reversal failure, deposit amount mismatch, excess-payment refund
  failure — the money-in-limbo sentinel) is gone with it: those lines still land
  in `/ctech-wallet/<env>/app`, but finding them is a Logs Insights query, not a
  page. Reinstate the alarm before anyone relies on being told.
- **Deploys replace the instances.** `.github/workflows/api.yml` uploads the
  artifact and starts an ASG instance refresh with `MinHealthyPercentage: 0` —
  no replacement is launched before the old instance goes away, so the service
  is **down** for the length of the refresh. `SkipMatching` stays `false`: a
  deploy does not change the launch template.
- **SSM agent is off by default** (`ENABLE_SSM_AGENT=true cdk deploy` puts it
  back for a debugging shell). Nothing needs RunCommand now, and the agent costs
  ~70 MiB of RSS on a t4g.nano. Flipping it replaces the instances.
- **The ASG runs 11:55 → 13:15 America/Sao_Paulo** and is scaled to zero outside
  that window: unreachable, inbound webhooks fail. A deploy outside the window
  exits early and the next scheduled instance boots the artifact.

## ReconcileStack (`reconcile-stack.ts`)

Lambda (`provided.al2023`, 256 MB, 5 min timeout), **not** in the VPC (uses its
own in‑memory locker; only needs DynamoDB + Inter‑via‑pix‑gateway + account).
`EventBridge Scheduler` every **5 min** (`RECONCILE_RATE_MINUTES`, `:34`) —
must stay well below the API's `sweepAgeThreshold` (10m) and `depositTTLMinutes`
(60m). Role touches `wallets, wallet_ledger_entries, wallet_idempotency,
wallet_pix_deposits, wallet_withdrawals, wallet_sandbox_purchases,
wallet_product_purchases, wallet_holds` (+indexes) and invokes pix‑gateway's
outbound Lambda. Deposit access is limited to read/update/query, and holds are
query-only. The deposit and purchase tables back lost-webhook fallback sweeps;
the holds status index backs stale-hold alarms. The process uses
`config.LoadReconcile`, so
API-only JWT issuer/CORS/Valkey startup guards do not prevent this non-HTTP job
from running.

## PixGatewayStack (`pix-gateway-stack.ts`)

- **Outbound** Lambda (20s): holds Inter mTLS keypair + OAuth secret
  (`ssm:GetParameter` on `inter/mtls-cert`, `inter/mtls-key`,
  `inter/client-secret`). Invoked synchronously by `api`.
- **Webhook** Lambda (10s): behind an **mTLS** API Gateway v2 HTTP API custom
  domain `pix.wallet.aoctech.app`, route **`POST /pix/webhook`** (`:186`),
  `disableExecuteApiEndpoint: true` so the custom domain is the only entry
  (`:183`). Holds **no Inter creds** — only its own M2M secret
  (`pix-gateway/client-secret`) + the Inter webhook hmac secret. mTLS CA in a
  versioned S3 trust store.

## FrontendStack (`frontend-stack.ts`)

`createNextjsStaticFrontend` from `@aoctech/cdk` creates S3 (OAC, block-public),
CloudFront, the rewrite function, KVS and security headers. The wallet stack adds
its locale negotiation rewrite. `API_PATH_PATTERNS` (`/v1.0/*`,
`/.well-known/*`) forwards API and OAuth resource metadata to the HAProxy API
origin **same‑origin** (no CORS needed);
`ALL_VIEWER_EXCEPT_HOST_HEADER` so the API gets the real `Authorization`/body.
Security response headers + CSP (`connect-src 'self' https://<accounts>`).

## OidcStack (`oidc-stack.ts`)

One‑time global stack: GitHub Actions deploy roles via OIDC (`repo:*`), **no
long‑lived keys**. Separate roles for frontend / api / reconcile (blast‑radius
isolation); the infra role gets `AdministratorAccess`. The additional
`ctech-wallet-gha-scopes` role reads only Account's app URL and Wallet's bound
publisher client ID/secret.

## Deploy flow

```bash
cd ../../ctech-cdk
CTECH_AWS_PROFILE=ctech ./scripts/configure-service-url-parameters.sh <env>
cd ../ctech-wallet/cdk
npm ci
npx cdk deploy CtechWallet-<Env>-DynamoDB      # then IAM, Api, Reconcile, PixGateway, Frontend
# (or the whole app) — per environment
```

The EC2 API resolves account transport/JWKS and wallet audience/CORS URLs from
SSM whenever the service starts. Updating them therefore requires an SSM change
plus service restart/instance refresh, not a template change. Internal transport
uses `*.internal.aoctech.app`; the OIDC issuer and browser URLs stay public.
Reconcile and pix-gateway Lambdas stay on public endpoints because they run
outside the shared VPC/private hosted zone.

CI: `.github/workflows/{api,frontend,infra,deploy}.yml`.

## Known divergences (documented, NOT fixed here)

| ID | Where | Status |
|----|-------|--------|
| **B1** | `dynamodb:TransactWriteItems` — **FALSE POSITIVE (closed)**. Not an IAM action; needs item‑level perms (`ConditionCheckItem`/`DeleteItem`/`PutItem`/`UpdateItem`) which the wallet IAM already grants. Money ops work in prod. | No fix needed. |
| — | OPERATIONS.md §4 instructs Inter to POST the webhook to `…/webhook?hmac=` but the CDK registers `POST /pix/webhook` (`pix-gateway-stack.ts:186`). Path mismatch. | Doc gap — align OPERATIONS.md / Inter registration. |
| — | OPERATIONS.md §4 omits the pix‑gateway webhook M2M secret `/ctech-wallet/{env}/pix-gateway/client-secret` (required by `ssm.go:20`, `constants.ts:144`). | Doc gap — seed it. |

## Cross‑links

- API: [`../api/README.md`](../api/README.md) · pix‑gateway:
  [`../pix-gateway/README.md`](../pix-gateway/README.md)
- Secrets / SSM seeding: [`../OPERATIONS.md`](../OPERATIONS.md)
- Wire contract: [`../rpc-contract/README.md`](../rpc-contract/README.md)
