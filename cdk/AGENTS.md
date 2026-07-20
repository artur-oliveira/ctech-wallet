# AGENTS.md — cdk (ctech-wallet-cdk)

Context for AI agents working on the wallet infrastructure. Identical intent
to [`CLAUDE.md`](CLAUDE.md); this is the agent-facing summary.

AWS CDK (TypeScript) for `ctech-wallet`. This stack custodies **real
third-party money** — the 12 Financial Safety Invariants in the repo root
[`../CLAUDE.md`](../CLAUDE.md) are non-negotiable.

## Stacks (`lib/*.ts`)

`DynamoDBStack` (8 tables + GSIs, OnDemand), `IAMStack` (API instance
role), `ApiStack` (EC2 ASG + ALB + nginx + alarm), `ReconcileStack`
(reconcile Lambda + EventBridge 5 min), `PixGatewayStack` (outbound +
webhook Lambdas, mTLS HTTP API), `FrontendStack` (S3 + CloudFront),
`OidcStack` (GitHub Actions OIDC roles). Shared VPC/ALB/Valkey come from
`ctech-cdk` via SSM.

## Rules (MUST follow)

- **Constants only** (`lib/constants.ts`, `lib/types.ts`) — no inlined
  resource names / SSM paths / ports / GSI names.
- **Table names/keys/GSIs MUST match `api/internal/domain/wallet/model.go`**
  exactly (`dynamodb-stack.ts:8-11`). A drift silently breaks every query.
- **No `dynamodb:Scan`** anywhere; GetItem/Query only.
- Append-only tables (`wallet_ledger_entries`, `wallet_audit` —
  `APPEND_ONLY_TABLES`, `constants.ts:178`) get a NARROWER IAM action set
  + explicit **DENY** `UpdateItem`/`DeleteItem` (Invariant #2/#10).
- Inter secrets live in **pix-gateway's** role, never the API role.

## B1 is a FALSE POSITIVE (closed)

`dynamodb:TransactWriteItems` is **not** an IAM action — a
`TransactWriteItems` call needs the item‑level permissions
(`ConditionCheckItem`/`DeleteItem`/`PutItem`/`UpdateItem`), which the
wallet IAM role **already grants**. Monetary transactions work in
production. No IAM change needed.

## Money

All money is **integer centavos**, computed in `api`. CDK stores exactly
what `api` writes — never round or transform values here.

## Cross-links

- API: [`../api/README.md`](../api/README.md), [`../api/ENDPOINTS.md`](../api/ENDPOINTS.md)
- pix-gateway: [`../pix-gateway/README.md`](../pix-gateway/README.md)
- Root invariants: [`../CLAUDE.md`](../CLAUDE.md)
- Secrets / SSM seeding: [`../OPERATIONS.md`](../OPERATIONS.md)
