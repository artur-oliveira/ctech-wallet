# pix-gateway: Move Inter PIX Integration to Lambda — Design

**Status:** proposed
**Date:** 2026-07-13
**Depends on:** `docs/specs/2026-07-12-three-wallet-topology-design.md` (wallet model unaffected by this
change — this spec only moves *how* `api` talks to the bank, not the ledger/wallet logic).
**Blocks:** nothing new; unblocks removing IPv4 cost from the `api` EC2/ALB path.

---

## Problem

Banco Inter's PIX API (and its webhook mTLS handshake) is IPv4-only. `api` runs on an EC2 ASG behind an
ALB that is intentionally **IPv6-only** (avoids AWS's per-hour IPv4 address cost). Today `api` reaches
Inter directly over mTLS from `internal/pix/inter.go`, which requires IPv4 egress the ALB/EC2 path
doesn't have cheaply, and the inbound webhook (`POST /internal/pix/webhook`) is only reachable today
because Cloudflare proxies `webhook-api.aoctech.app` in front of the IPv6-only ALB — which also means
Cloudflare terminates the inbound TLS connection, so Inter's mTLS client certificate never reaches `api`
undisturbed (Cloudflare's own mTLS/client-cert verification is an Enterprise-tier feature we don't have).

Lambda gets cheap IPv4 egress (no NAT, no per-address charge) when not VPC-attached, and AWS API Gateway
supports mTLS client-certificate verification natively on a regional custom domain. Moving all direct
contact with Inter into a small dedicated module solves both the outbound egress cost and the inbound
mTLS problem in one piece, without touching the ALB or Cloudflare configuration at all.

## Solution

A new sibling module, **`pix-gateway/`** (Go, alongside `api/`, `cdk/`, `ui/`), owns every direct
interaction with Inter: outbound PIX calls (charge, query, DICT, transfer, refund, ping) and the inbound
webhook. `api` keeps all ledger, balance, and business logic — it stops making direct bank calls but does
not lose any responsibility over money movement.

### Outbound calls: `api` → Lambda (synchronous)

`api`'s `internal/pix.PixClient` interface (`client.go`) is unchanged — `services/wallet.go` and
`services/reconcile.go` keep calling it exactly as today. Only the implementation swaps:

- **Today:** `InterClient` in `api/internal/pix/inter.go` — direct mTLS HTTP calls to Inter.
- **After:** `LambdaPixClient` in `api/internal/pix/lambda_client.go` — marshals each of the 7 interface
  methods into `{"op": "<MethodName>", "payload": {...}}`, invokes `pix-gateway`'s Lambda synchronously
  (`lambda:InvokeFunction`, `RequestResponse`), unmarshals the result. Same call shape, same error
  semantics (`statusError`-equivalent surfaces through `problem.*` exactly as it does today) — this is a
  transport swap, not a behavior change.
- `inter.go`'s actual Inter-calling code (paths, OAuth token manager, money conversion helpers) moves
  verbatim into `pix-gateway/internal/inter/` — not reimplemented. `fake.go` stays in `api/internal/pix`
  for `api`'s own unit tests against the interface; it never talks to the Lambda.
- The Inter mTLS keypair and OAuth client secret (SSM SecureString) move to `pix-gateway`'s own IAM
  role/SSM path. `api`'s EC2 role loses `ssm:GetParameter` on `/ctech-wallet/{env}/inter/mtls-{cert,key}`
  entirely — it never touches Inter credentials again.
- `api`'s EC2 role gains `lambda:InvokeFunction` scoped to the `pix-gateway` function ARN.

### Inbound webhook: Inter → API Gateway (mTLS) → Lambda → `api` internal endpoint

Per Bacen's PIX spec and Inter's webhook docs, Inter is the mTLS *client* when delivering a webhook — it
presents a client certificate signed by a CA Inter provides (the `.crt` downloaded at webhook
registration), and the receiving server chooses which CA it trusts. AWS API Gateway (**HTTP API**, chosen
over REST API — proxy-only integration, lower cost/latency, and HTTP APIs support mTLS custom domains
too) supports this directly via a **Trust Store**: a CA bundle in a versioned S3 bucket, attached to a
**regional** custom domain (mTLS custom domains cannot be edge-optimized).

- New subdomain `pix.wallet.aoctech.app`: an API Gateway **custom domain name** resource is created for
  it (regional, mTLS-enabled), which generates its own target regional domain name. The Cloudflare DNS
  record for `pix.wallet.aoctech.app` is a CNAME to that generated target and **must be DNS-only (grey
  cloud, unproxied)** — if Cloudflare proxies it, Cloudflare terminates the TLS handshake itself and
  Inter's client certificate never reaches API Gateway, the same failure mode this design exists to avoid
  at the ALB. The domain still needs its own ordinary ACM server certificate (regional, standard HTTPS,
  unrelated to Inter's client cert), issued for use with the custom domain.
- The Trust Store holds Inter's webhook CA/certificate. API Gateway rejects any TLS handshake that
  doesn't chain to it — untrusted calls never reach Lambda.
- The webhook Lambda handler **never trusts the payload** (existing invariant, preserved): on receipt it
  re-queries the charge from Inter (via the same outbound Inter client code, in-process — no extra
  Lambda-to-Lambda hop) to confirm amount, status, and payer CPF.
- Once confirmed, it calls a **new internal endpoint on `api`**, `POST
  /internal/pix/confirm-deposit`, over the public domain (`wallet.aoctech.app` — already CloudFront-fronted,
  dual-stack; the Lambda is not VPC-attached so it gets normal internet egress and needs no special
  networking to reach it). Authenticated the same way every other internal caller is: M2M
  `client_credentials` JWT, scoped e.g. `internal:pix:confirm-deposit`.
- `api`'s existing `pixWebhook` handler, the `X-Webhook-Secret` check, and `POST
  /internal/pix/webhook` route are deleted. (That header check was never going to work in production —
  neither Inter's nor Bacen's docs describe Inter sending a shared-secret header; the real authentication
  is the mTLS handshake now enforced at the API Gateway edge.) `confirm-deposit` does exactly what the
  old handler did *after* its re-query step: credit the ledger, run KYC promotion on first deposit.

### Real-time notification (new)

`api` gains a WebSocket channel so a user sees their deposit land without polling, mirroring
`ctech-dfe`'s pattern (`internal/ws`: `Registry` interface, `MemoryRegistry`, `RedisRegistry` using
Valkey pub/sub for fan-out across ASG instances — no sticky sessions, no API Gateway WebSocket, no
connection table):

- `internal/ws/{registry.go,memory.go,redis.go}` ported into `api`, keyed by `user_id` (JWT `sub`)
  instead of `org_pk`.
- `GET /ws?token=<jwt>` upgrade endpoint (`api/v1/ws.go`), registers the connection under the user's ID.
- After `confirm-deposit` commits the ledger credit, `WalletService` broadcasts
  `{"type": "deposit_confirmed", "wallet_id", "amount", ...}` to that user's registry key. Any
  ASG instance holding that user's socket delivers it via the Valkey channel `ws:{user_id}`.

### UI: WebSocket connection + polling fallback

`ui` ports `ctech-dfe`'s hook pair, adapted from org-scoped to user-scoped:

- `useWebSocket` (`ui/src/lib/hooks/useWebSocket.ts`) — copied essentially as-is (generic reconnect
  logic keyed by URL, exponential backoff, ping/pong keepalive; nothing org-specific in this hook).
- `useWalletRealtime` (new, mirrors `useRealtimeUpdates`) — builds `GET /v1.0/ws?token=<jwt>` (no
  `org_pk`, the wallet has no organization concept), and on `deposit_confirmed` invalidates the
  `['balances']` and `['ledger']` React Query caches and fires a success toast. Connected once at the
  dashboard level (`DashboardInner`), same lifetime as the session, not scoped to a single charge — so it
  also covers a deposit confirmed while the charge dialog isn't even open.

**Polling fallback (`PixChargeDialog`):** the WebSocket is the primary path, but the dialog does not
depend on it exclusively — the socket can be down, reconnecting, or the browser tab backgrounded and
throttled. `PixChargeDialog` starts a timer on mount; if the charge hasn't resolved within **30s**, it
begins polling `['balances']` (`refetchInterval`, e.g. every 5s) until either:
  - a `deposit_confirmed` WebSocket message arrives for this charge (checked by comparing the
    broadcast wallet/amount against the open charge — whichever arrives first wins), or
  - the polled balance reflects the credit,

at which point the dialog closes itself and shows the same success toast the WebSocket path would have
shown, and polling stops. If the charge's own 15-minute expiry (`chargeExpirySec`) elapses first, polling
stops and the dialog reverts to its current expired-charge state. No new backend endpoint is needed for
this — it reuses the existing `GET /wallet/balances` the dashboard already polls-on-demand via
`apiClient.getBalances()`.

### Resilience — no SNS/SQS added

Inter already retries a failed webhook delivery itself (4 attempts: 20/30/60/120 min, per Inter's docs).
Combined with the re-query-before-trust rule, a dropped webhook only delays confirmation — it can never
cause a false credit. This is judged sufficient: no SNS/SQS fan-out is added for the webhook path. The
existing withdrawal reconciliation job (`ReconcileWithdrawals`) is untouched by this spec.

### CI/CD

New `.github/workflows/pix-gateway.yml`, mirroring `ctech-dfe`'s `worker.yml`: test → build Go arm64
binary(ies) → zip → upload to the environment's deployments S3 bucket → `aws lambda
update-function-code` → wait for `function-updated`. Wired into `deploy.yml`'s existing ordering ahead of
`api` (the API's Lambda-invoke path depends on the function existing).

### CDK changes

- Remove `mtls-cert`/`mtls-key` SSM read permission from the `api` EC2 role (`api-stack.ts`).
- New stack/construct for `pix-gateway`: Lambda function (Go arm64, no VPC attachment), HTTP API with
  regional mTLS custom domain (`pix.wallet.aoctech.app`) backed by a versioned S3 Trust Store bucket
  seeded with Inter's webhook CA.
- Grant `api`'s EC2 role `lambda:InvokeFunction` on the `pix-gateway` function.
- No changes to the ALB, Cloudflare, or `webhook-api.aoctech.app` (that subdomain is retired once the
  webhook route moves to `pix.wallet.aoctech.app`).

---

## Non-goals

- No change to ledger, balance, idempotency, or locking logic — those stay exactly as specified in the
  root Financial Safety Invariants and untouched in `api`.
- No change to the three-wallet topology or gambling ring-fence.
- No SNS/SQS webhook fan-out (see Resilience above).
- No change to withdrawal reconciliation.

## Open items to confirm against Inter's dashboard/docs before implementation

- Confirm whether Inter's webhook delivery ever also sends a shared-secret header (docs found so far
  describe only mTLS); if it does, `confirm-deposit`'s caller-auth can keep it as defense-in-depth.
- Confirm the exact format of the downloaded webhook `.crt` (single self-signed cert vs. issuing CA) to
  know whether the Trust Store holds it directly or a separate CA bundle.
