# ctech-wallet CDK

AWS CDK (TypeScript) infrastructure for the wallet. Deploys: DynamoDB tables,
the API on an EC2 ASG behind an ALB, the reconcile Lambda, the `pix-gateway`
Lambdas, the static frontend (S3 + CloudFront), and the GitHub‑Actions OIDC
deploy roles.

Entry: `bin/ctech-wallet-cdk.ts` (`cdk.json` → `npx ts-node --prefer-ts-exts
bin/ctech-wallet-cdk.ts`). Per‑environment; shared infra (VPC, ALB, Valkey) is
owned by `ctech-cdk` and referenced via SSM.

## Stacks

| Stack | File | Provisions |
|-------|------|-----------|
| `DynamoDBStack` | `lib/dynamodb-stack.ts` | 8 tables + GSIs (OnDemand) |
| `IAMStack` | `lib/iam-stack.ts` | EC2 instance role for the API |
| `ApiStack` | `lib/api-stack.ts` | EC2 ASG + ALB + nginx + deploy scripts + CloudWatch alarm |
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
| `wallet_idempotency` | — | `IDEM#{key}` guards; **TTL** 7d |
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

## ApiStack — EC2 ASG + ALB (`api-stack.ts`)

- Reuses `@aoctech/cdk`'s `PrivateIpv4Ec2Service` (shared VPC, no NAT). ALB
  listener priority **35** (`constants.ts:41`); health path
  `/v1.0/health-check`, healthy codes **200,207** (`:463`) — matches the API's
  degraded‑`207` contract.
- Instances: min 1, max 3 (prod). nginx `:8080` → app `:8000`
  (`constants.ts:44-48`). Rate limit `100r/s` by real viewer IP
  (`limit_req_zone`, `:182`). WebSocket `/v1.0/ws` upgrade proxied
  (`:223`). Real‑IP resolved via `update-realip.sh`.
- **User data** (`api-stack.ts:96`) writes nginx.conf, systemd `app.service`,
  `start.sh` (fetches non‑secret env + reads secrets from SSM at boot:
  `VALKEY_URL` DB **2**, `CTECH_URL`/`CTECH_JWKS_URL`, `WALLET_CLIENT_ID`/
  `SECRET`), `deploy.sh` (SSM RunCommand rolling deploy invoked by GitHub
  Actions), `upload-logs.sh`, logrotate.
- CloudWatch **alarm on `"ALARM"` log lines** (`:485`) — fires on refund/
  reversal failures, deposit amount mismatch, excess‑payment refund failure
  (the money‑in‑limbo sentinel).

## ReconcileStack (`reconcile-stack.ts`)

Lambda (`provided.al2023`, 256 MB, 5 min timeout), **not** in the VPC (uses its
own in‑memory locker; only needs DynamoDB + Inter‑via‑pix‑gateway + account).
`EventBridge Scheduler` every **5 min** (`RECONCILE_RATE_MINUTES`, `:34`) —
must stay well below the API's `sweepAgeThreshold` (10m) and `depositTTLMinutes`
(60m). Role touches `wallets, wallet_ledger_entries, wallet_idempotency,
wallet_withdrawals` (+indexes) and invokes pix‑gateway's outbound Lambda.

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

S3 (OAC, block‑public) + CloudFront. Next.js static export served at the edge;
a CloudFront Function rewrites clean URLs to `.html` using a **KeyValueStore**
route manifest published by the frontend workflow. `API_PATH_PATTERNS`
(`/v1.0/*`) forwards to the ALB origin **same‑origin** (no CORS needed);
`ALL_VIEWER_EXCEPT_HOST_HEADER` so the API gets the real `Authorization`/body.
Security response headers + CSP (`connect-src 'self' https://<accounts>`).

## OidcStack (`oidc-stack.ts`)

One‑time global stack: GitHub Actions deploy roles via OIDC (`repo:*`), **no
long‑lived keys**. Separate roles for frontend / api / reconcile (blast‑radius
isolation); the infra role gets `AdministratorAccess`.

## Deploy flow

```bash
cd cdk && npm ci
npx cdk deploy CtechWallet-<Env>-DynamoDB      # then IAM, Api, Reconcile, PixGateway, Frontend
# (or the whole app) — per environment
```

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
