# Deploy capacity: wallet

Derived from `cdk/lib/api-stack.ts`, `cdk/lib/dynamodb-stack.ts`, `cdk/lib/reconcile-stack.ts` and the shared `HaproxyEc2Service` in `@aoctech/cdk` (ctech-cdk). The CDK is the source of truth; update this page when it changes.

## API (EC2 ASG)

| Item | Value |
|---|---|
| Instance type | `t4g.nano` (arm64, 0.5 GiB), the `HaproxyEc2Service` default; no `instanceType` override |
| Purchase | On-demand (`onDemand: true`), no Spot, no capacity rebalance |
| ASG | min 1, max 2, desired = min; the extra instance exists only for rolling replacement |
| Rolling update | `minInstancesInService = min(min, max-1) = 1`: the old instance serves until the new one is healthy |
| Image / disk | Alpine arm64 AMI from SSM (`CtechSSM.amiAlpine`), root volume 3 GiB gp3 (shared default), 256 MB swap |
| Process layout | nginx in front of two app processes (`APP_PORT` and `APP_PORT_ALT`, logs `app.log` and `app2.log`); `GOMEMLIMIT` is 75% of RAM split across them |
| Schedule | None (the daytime-window `schedule` is commented out): always on |
| Logs | CloudWatch Logs only (`app`, `nginx` groups); prod log groups retained |

## Lambdas

- `reconcile` (`cdk/lib/reconcile-stack.ts`): `provided.al2023` arm64, 256 MB, 5 min timeout, EventBridge Scheduler every 5 minutes. Resolves withdrawals stuck in `processing` and alerts when a reversal credit-back fails.
- Erasure consumer runs inside the API (SQS `{env}-wallet-erasure`, DLQ after 5 receives); see `dlq-redrive.md`.

## DynamoDB

- On-demand (`PAY_PER_REQUEST`) with an explicit ceiling of **1000 RRU / 1000 WRU per table and per GSI** (`maxReadRequestUnits` / `maxWriteRequestUnits`). Requests above it are throttled, not billed.
- PITR enabled in prod only. Tables are `RETAIN` outside dev (`DESTROY` in dev); retired and restricted tables are always `RETAIN`.
- TTL attribute `ttl` on temporary tables (HTTP idempotency, erasure state, retention, sandbox purchases, self-exclusion carry).
- Existing tables are never recreated; Terraform import is a separate change (ctech-skills `docs/specs/2026-10-24-dynamodb-terraform-import.md`).

## Known limits

- One `t4g.nano` serves all traffic. During a deploy, or when the instance is lost, capacity is the surviving instance only (max 2). There is no CPU-based scaling policy: scale by raising min/max in CDK.
- Before running more than one instance for load (not just during replacement), confirm in-process loops (reconcile triggers, sweeps) are safe across instances; the ledger itself relies on DynamoDB conditional writes.
- 0.5 GiB RAM with 256 MB swap leaves little headroom: do not add in-process caches without measuring.
- The 1000 RRU/WRU ceiling throttles bursts above roughly 1000 1 KB writes or 1000 strongly consistent 4 KB reads per second per table; a hot ledger partition throttles earlier.
