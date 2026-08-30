# ctech-wallet — Operations

Out-of-band steps that live in **ctech-account**, not in this repo. The wallet API
needs them before it can promote KYC or accept sandbox M2M traffic.

## 0. Create/update service URL parameters

Before deploying the EC2 API, run the shared idempotent helper from `ctech-cdk`:

```bash
CTECH_AWS_PROFILE=ctech ./scripts/configure-service-url-parameters.sh prod
```

It writes wallet/account/poker/DFE URL parameters with the private
`*.internal.aoctech.app` hosts for EC2-to-EC2 transport and JWKS retrieval.
Public application URLs remain public because they are OAuth issuer, audience,
CORS and browser contracts. After changing a URL parameter, restart the service
or refresh the instance; a CDK template change is not needed.

## 1. Publish the Wallet Resource Server manifest

The Wallet owns `api/internal/oauthresource/scope-manifest.json`. The deploy
workflow publishes it to CTech Account through the Wallet-bound confidential
scope publisher before deploying the API. The manifest currently contains 13
public `wallet:*` permissions and 10 service-only `internal:wallet:*`
permissions; no Account source-code edit or legacy `seedscopes` execution is
required.

### 1a. First-party UI client grant

The Account registry automatically appends every active public manifest scope
to an existing first-party public OAuth client whose ID equals the Resource
Server ID. Consequently, publishing `wallet` reconciles the standard `wallet`
client while preserving its redirect URIs, audience and existing identity
grants. Its resulting `allowed_scopes` contains:

```text
openid
profile
kyc
wallet:state:read
wallet:terms:write
wallet:balances:read
wallet:ledger:read
wallet:deposits:write
wallet:withdrawals:write
wallet:sandbox-purchases:read
wallet:sandbox-purchases:write
wallet:product-purchases:read
wallet:game:write
wallet:gambling:read
wallet:gambling:write
wallet:custody:write
```

The client must already exist as `public` + `first_party`; the publisher never
creates/promotes clients and never grants scopes to any differently named
client or API key. CTech Account clamps `/authorize` to `allowed_scopes`, so
deploy order remains scopes before UI. After publication, start a fresh
authorization flow (not only a refresh) so an existing browser session receives
an access token containing `wallet:*`.

## 2. Seed the wallet's own M2M client

So the wallet can call account's `internal:account:kyc` (confirm on first deposit, read CPF).
Direct DynamoDB put into `{env}_account_oauth_clients` (`pk=CLIENT_<id>`), exactly:

- confidential (has a client secret)
- `first_party: true`
- `allowed_scopes: ["internal:account:kyc"]`

Set the wallet's `WALLET_CLIENT_ID` / `WALLET_CLIENT_SECRET` env to match.

## 3. Seed each consumer app's M2M client

Poker, dominó, and future billing each get a confidential M2M client whose
`allowed_scopes` is only the subset they need:

- poker/dominó → `internal:wallet:credit`, `internal:wallet:debit`
- billing (future) → `internal:wallet:debit`

`ctech-account`'s token endpoint clamps requested scope to `allowed_scopes`, so a
client can never request more than it was granted.

## 4. Inter (PIX) partner-bank configuration — SSM SecureString

Nothing here is an env var in the repo and nothing is committed. Seed these SSM
parameters per environment (`dev` / `stage` / `prod`):

| Parameter                                  | Type             | Read by                             |
|--------------------------------------------|------------------|-------------------------------------|
| `/ctech-wallet/{env}/inter/mtls-cert`      | **SecureString** | `pix-gateway` (SDK)                 |
| `/ctech-wallet/{env}/inter/mtls-key`       | **SecureString** | `pix-gateway` (SDK)                 |
| `/ctech-wallet/{env}/inter/client-id`      | String           | `pix-gateway` `start.sh` → `INTER_CLIENT_ID`      |
| `/ctech-wallet/{env}/inter/client-secret`  | **SecureString** | `pix-gateway` `start.sh` → `INTER_CLIENT_SECRET`  |
| `/ctech-wallet/{env}/inter/webhook-secret` | **SecureString** | `pix-gateway` `start.sh` → `INTER_WEBHOOK_SECRET` |
| `/ctech-wallet/{env}/wallet-client-id`     | String           | `start.sh` → `WALLET_CLIENT_ID`     |
| `/ctech-wallet/{env}/wallet-client-secret` | **SecureString** | `start.sh` → `WALLET_CLIENT_SECRET` |

The **mTLS keypair is deliberately not an env var**: `pix-gateway` reads it from SSM with
the SDK and holds it in memory, so the bank certificate can be
rotated without a redeploy and the PEM never touches the disk or `/proc/<pid>/environ`.
The wallet `api` does **not** read any Inter secret — it only reads its own M2M
`WALLET_CLIENT_ID`/`WALLET_CLIENT_SECRET` and `ctech-account`'s base/JWKS URLs (from SSM
or env), never `mtls-cert`/`mtls-key`/`INTER_*`.
Cert and key are separate parameters because a standard-tier SecureString caps at 4 KB.

Write them like this (example):

```bash
aws ssm put-parameter --type SecureString --overwrite \
  --name "/ctech-wallet/prod/inter/mtls-cert" --value "file://inter-cert.pem"
aws ssm put-parameter --type SecureString --overwrite \
  --name "/ctech-wallet/prod/inter/mtls-key"  --value "file://inter-key.pem"
```

> These use the AWS-managed `alias/aws/ssm` KMS key, whose default grants cover
> `--with-decryption` for the instance role. **If you ever move them to a
> customer-managed KMS key, you must add an explicit `kms:Decrypt` statement to the
> instance role** — otherwise the app fails to boot.

Also register the webhook secret with Inter's webhook configuration, and set
`INTER_PIX_KEY` (the receiving key for charges) in the API stack's static env.
Register the webhook URL with Inter as `https://pix.wallet.aoctech.app/webhook?hmac=<the
same value stored in /ctech-wallet/{env}/inter/webhook-secret>` — Inter echoes this query
string back on every callback and `pix-gateway`'s webhook Lambda now rejects any request
where it doesn't match.

> Before enabling real money, confirm each Inter endpoint's request/response shape
> against Inter's current API reference and sandbox (see `api/internal/pix/inter.go`).

### Network caveat (verify before go-live)

The shared VPC has **no NAT gateway and no public IPv4** on instances — egress is
IPv6/dual-stack only. Every existing service only calls AWS and SEFAZ. The wallet is
the first to call a **third-party API (Inter)**. If Inter's API is IPv4-only, the
outbound PIX calls will not leave the instance. Confirm Inter's connectivity before
relying on this network design.

## 4a. M2M sandbox-purchase client registry (e.g. ctech-poker) — SSM SecureString

Optional. Only needed once an M2M client is granted `internal:wallet:sandbox-purchase` and needs its purchase
confirm/refund notify-back delivered (see
`docs/specs/2026-07-30-m2m-sandbox-purchase-integration-design.md`). Unset is a valid "no M2M client registered
yet" state — `api` and `cmd/reconcile` both boot fine without it.

| Parameter                          | Type             | Read by                     |
|-------------------------------------|------------------|------------------------------|
| `/ctech-wallet/{env}/m2m-clients`  | **SecureString** | `api`, `cmd/reconcile` (SDK) |

Value is a JSON object keyed by the client's OAuth `client_id` (the JWT's `azp` claim):

```bash
aws ssm put-parameter --type SecureString --overwrite \
  --name "/ctech-wallet/prod/m2m-clients" \
  --value '{"poker": {"WebhookURL": "https://poker.aoctech.app/internal/wallet-webhook", "HMACSecret": "<random-32-byte-hex>"}}'
```

`HMACSecret` is whatever random value you hand to the receiving service too — the wallet signs every notify-back
POST with `X-Wallet-Signature: sha256=hex(HMAC-SHA256(body, HMACSecret))` and the receiver must verify it with the
same secret. Rotate by updating both sides together; there is no versioning/overlap window.

## 4b. Asaas custody (BaaS) — required before any deposit works

Deposits have exactly one rail: a PIX static QR on the user's own Asaas
subaccount. There is no Inter fallback, so an unresolvable parameter here means
deposits are refused, never rerouted (`docs/specs/2026-08-30-asaas-only-deposits.md`).

| Parameter                                                | Type             | Read by                    | What it is |
|----------------------------------------------------------|------------------|----------------------------|------------|
| `/ctech-wallet/{env}/asaas/api-key-master`               | **SecureString** | `api` (SDK, at boot)       | Hex-encoded AES-256 key. Every subaccount API key is stored AES-256-GCM encrypted under it and under nothing else — losing it makes every subaccount unusable, so back it up outside this account before onboarding anyone. |
| `/ctech-wallet/{env}/asaas/parent-api-key`               | **SecureString** | `api`, `cmd/reconcile`     | CTech's own master-account API key. Authenticates subaccount creation, the fee charge, and the §9.1a reversal leg. |
| `/ctech-wallet/{env}/asaas/webhook-token`                | **SecureString** | `api` (SDK, at boot)       | The static token Asaas echoes in `asaas-access-token`. Empty refuses every webhook — it never degrades to "any token". |
| `/ctech-wallet/{env}/asaas/parent-wallet-id`             | String           | `api`, `cmd/reconcile` env | Destination `walletId` for every settlement leg. |
| `/ctech-wallet/{env}/asaas/master-account-id`            | String           | `api` env                  | CTech's account id at Asaas. |
| `/ctech-wallet/{env}/asaas/master-pix-key`               | String           | `api` env                  | The master account's static EVP key — the verification-fee QR is built on it. `api` refuses to boot in production without it. |
| `/ctech-wallet/{env}/asaas/verification-fee-cents`       | String           | `api` env                  | One-off subaccount verification fee, centavos (`1290` = R$ 12,90). Tracks Asaas's own price; change it here, no deploy. |

Non-SSM knob: `ASAAS_FREE_RECEIPTS_PER_MONTH` (default `95`). Asaas gives every
account 100 free PIX receipts per calendar month and bills each one after that;
the margin covers charges opened but not yet paid. Raising it past 100 buys
per-receipt costs, not throughput.

### Webhooks to configure in the Asaas panel

Both are ordinary API routes on the main API — the mTLS HTTP API exists only
because Inter requires a client certificate.

| Event family | URL |
|---|---|
| Account status, payments, MED | `POST https://wallet-api.aoctech.app/v1.0/internal/asaas/webhook` |
| Transfer authorization (synchronous) | `POST https://wallet-api.aoctech.app/v1.0/internal/asaas/transfer-authorization` |

Set the access token on both to the `webhook-token` value above. Transfer
authorization must be enabled explicitly in the panel — until it is, outbound
transfers are not validated against the intent row the wallet wrote.

### Rollout order

1. Publish `pix-gateway` **before** `api`: the account-status and pending-document
   ops are new on the gateway, and without them no subaccount ever reaches
   `approved`.
2. Seed every parameter above; confirm the panel webhooks answer.
3. Set `custody_enabled: true` on exactly one real wallet in DynamoDB. That
   attribute is the onboarding allowlist — it gates who may open a subaccount,
   which is the scarce, paid action. It is admin-only and has no API write path.
4. Walk that user end to end: fee paid → subaccount approved → EVP key created →
   real deposit → real withdrawal → conservation check clean.
5. Only then add the next user.

**Asaas regulatory evaluation window.** From the first subaccount created in
production: at most 10 subaccounts, at most R$ 2.000,00 in charges per
subaccount, at most 60 corridos days. Hitting any of the three blocks further
subaccount creation and charge issuance until homologation. Asaas also requires
its brand, links, and responsibility text on every screen shown to the account
holder — `ui` carries this on the real card's deposit area.

## 4c. Log shipping: blank lines used to stop it dead

Symptom: `/var/log/app/*.log` exists and is filling, and nothing reaches
CloudWatch. `logs-nginx` streams fine, `logs-app` does not.

Diagnosis, in three reads over SSM:

```bash
# 1. Is the shipper alive? The number in parentheses is its restart count.
rc-status --all | grep ctech-ec2-agent-logs
# 2. Is its cursor advancing? A frozen offset:0 means it never flushed once.
for f in /var/lib/ctech-ec2-agent/*.pos; do echo "$f: $(cat $f)"; done
# 3. Why is it dying? Run it in the foreground and read the API's own words.
timeout 12 /usr/local/bin/ctech-ec2-agent logs-tail -config /etc/ctech-ec2-agent/logs-app.json
```

Root cause: `PutLogEvents` rejects a zero-length message **and fails the whole
batch** over one bad member, so a single blank line makes every other line in
its batch unshippable. Blank lines came from Fiber's ASCII startup banner and
fx's console-formatted tree, both written straight to stdout — which is the log
file. Fixed at both ends: `api/cmd/server/main.go` routes fx through slog and
`internal/app/app.go` passes `DisableStartupMessage`, while
`ctech-cdk/assets/ctech-ec2-agent/logstail.go` now drops unshippable lines,
treats a validation rejection as a poison batch instead of a reason to die, and
flushes before advancing its cursor.

**This was never wallet-specific.** Whether a blank line is fatal depended on
which flush path it landed in: a full 100-line batch flushes before saving the
cursor (fatal, permanent loop), a partial batch saves the cursor first (dies
once, then silently skips the batch it could not send). Services that looked
healthy were losing a batch of logs per blank line. The agent fix needs the
binary republished and the AMI rebuilt, then an instance refresh, for every
Alpine service.

Unsticking an instance already in the loop, without waiting for the AMI —
truncate in place (`:> file`) rather than `mv`, because `supervise-daemon` holds
the fd in append mode and the cursor is keyed on the inode:

```bash
cp -a /var/log/app/app.log /tmp/app.log.bak   # setup-logs.sh also archives to S3
: > /var/log/app/app.log
: > /var/log/app/app2.log
rc-service ctech-ec2-agent-logs-app restart
```

## 5. Withdrawal reconciliation schedule

Run `cmd/reconcile` on a schedule (e.g. EventBridge every 5 min). It resolves
withdrawals stuck in `processing` (completes or reverses) and exits non-zero when a
reversal's credit-back fails (`refund_failed`) so the scheduler raises an alarm.
It also re-queries aged pending PIX deposits, sandbox purchases, and product
purchases as a lost-webhook fallback. The Lambda role must include each purchase
table, the PIX deposit table, and their status GSIs. It also queries the holds
table status GSI for stale game holds.
`cmd/reconcile` uses `config.LoadReconcile`; it does not require the API server's
JWT issuer, CORS, or fleet-wide Valkey settings.

## 6. `GAMBLING_ENABLED` — do not turn this on yet

`GAMBLING_ENABLED` (default **`false`**) gates everything that moves money **into** the gambling ring-fence:
activation (`POST /wallet/gambling/activate`) and funding (`POST /wallet/game/deposit`). With the flag off those
routes are **not registered at all** — they 404. That is deliberate: an absent route cannot be reached by a bug, a
stale client, or a forgotten check.

`POST /wallet/game/withdraw` — the way **out** of the ring-fence — is deliberately **not** gated. The `game`
balance is real money (Invariant #9), so a route out must always exist: flipping the flag off must never strand a
user's own money in a game wallet. Reducing exposure is never blocked.

**Precondition for turning it on — all of them, no exceptions:**

1. The **personal limit engine is live** (daily/weekly/monthly caps on `real → game`, hierarchy validation,
   asymmetric cooldown). Activating a user into a gambling wallet with **no limits configured** is the single
   thing this design exists to prevent. Shipping activation with limits "to follow" is not an acceptable
   intermediate state.
2. `docs/legal/wallet-gambling-addendum.md` has passed **legal review** (it currently carries a PENDING banner),
   and the text, the UI page (`ui/src/app/gambling-addendum/page.tsx`), and
   `wallet.CurrentGamblingAddendumVersion` all agree.
3. The `wallet_audit` table exists in the target environment and the API role can write it.

Turning the flag on does **not** retroactively activate anyone: activation stays opt-in and per-user, gated on
verified KYC plus explicit acceptance of the gambling addendum.

Turning it back **off** is safe as a kill switch: it stops new money entering the ring-fence and stops new
activations, but does not delete anyone's `game`/`sandbox` wallet or their balances, and users can still return
their money to `real` (and from there withdraw by PIX) because that route is never gated.
