# ctech-wallet — Operations

Out-of-band steps that live in **ctech-account**, not in this repo. The wallet API
needs them before it can promote KYC or accept sandbox M2M traffic.

## 1. Seed the wallet scopes into the global catalog

The wallet defines two internal scopes its callers (poker/dominó, future billing)
use. They must exist in the global cross-service scope catalog.

1. Add them to `ctech-account/internal/scopes/catalog.go` in the `internal` family
   (`Internal: true`), alongside `internal:account:kyc`:
    - `internal:wallet:credit` — "grant sandbox currency"
    - `internal:wallet:debit` — "spend sandbox currency"
2. Run the account seeder:
   ```bash
   AWS_REGION=<r> TABLE_PREFIX=<env>_ VALKEY_URL=<url> \
     go run ./cmd/seedscopes   # from ctech-account
   ```
   It writes the catalog to `{env}_ctech_scopes` and invalidates the `scope_catalog`
   Valkey cache.

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
