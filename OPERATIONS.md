# ctech-wallet — Operations

Out-of-band steps that live in **ctech-account** and **ctech-billing**, not in this repo. The wallet API
needs them before it can read KYC, call Billing or accept M2M traffic.

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
scope publisher before deploying the API. The manifest currently has 6
active public `wallet:*` permissions and 6 active service-only `internal:wallet:*`
permissions (the rest are `deprecated`, see below); no Account source-code edit or legacy `seedscopes` execution is
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
```

`wallet:deposits:write` / `wallet:withdrawals:write` guard the Pix deposit and the withdrawal (through Billing).
The scopes of the retired routes (`wallet:custody:write`, `wallet:sandbox-purchases:*`, `wallet:product-purchases:read`,
`wallet:game:write`, `wallet:gambling:*` and the internal `confirm-deposit`, `sandbox-purchase`, `product-purchase`,
`charge-amount`, `game-hold`, `game-cashout`, `game-status`, `responsible-export`) stay in the manifest as `deprecated`:
the registry refuses to drop an active scope. Do not request them; a later change removes them.

The client must already exist as `public` + `first_party`; the publisher never
creates/promotes clients and never grants scopes to any differently named
client or API key. CTech Account clamps `/authorize` to `allowed_scopes`, so
deploy order remains scopes before UI. After publication, start a fresh
authorization flow (not only a refresh) so an existing browser session receives
an access token containing `wallet:*`.

## 2. Seed the wallet's own M2M client

So the wallet can call account's `internal:account:kyc` (read the verified CPF and KYC level; KYC promotion is manual-review-only on account's side).
Direct DynamoDB put into `{env}_account_oauth_clients` (`pk=CLIENT_<id>`), exactly:

- confidential (has a client secret)
- `first_party: true`
- `allowed_scopes: ["internal:account:kyc"]`

Set the wallet's `WALLET_CLIENT_ID` / `WALLET_CLIENT_SECRET` env to match.

## 2a. Seed the wallet's erasure ack client

The account-deletion saga needs a **dedicated** confidential client (not the KYC client of §2): a direct DynamoDB put
into `{env}_account_oauth_clients` with `first_party: true` and `allowed_scopes: ["internal:account:erasure-ack"]`
(nothing else). Store its id and secret in SSM `/ctech-wallet/{env}/erasure-client-id` and
`/ctech-wallet/{env}/erasure-client-secret` (SecureString). The KYC client is **not** granted the ack scope.

## 3. Seed each consumer app's M2M client

Each consumer app (Poker, dominó) gets a confidential M2M client whose
`allowed_scopes` is only the subset they need:

- Sandbox clients → `internal:wallet:credit`, `internal:wallet:debit`
- Clients that stake or spend `real` (Poker) → `internal:wallet:hold` and/or `internal:wallet:debit-real`, `internal:wallet:balance` (read)
- Never grant `debit-real` to a client that only needs Sandbox credit/debit.

Which clients exist today and what each is granted lives in `ctech-account`, not here (not verified from this repo).

`ctech-account`'s token endpoint clamps requested scope to `allowed_scopes`, so a
client can never request more than it was granted.

## 3a. Billing (the only rail)

Deposits and withdrawals go through ctech-billing, which owns the Inter connection. There is no flag: the API and the
reconcile Lambda refuse to start without `BILLING_URL`, `BILLING_CLIENT_ID`, `BILLING_CLIENT_SECRET` and
`BILLING_EVENT_SECRET` (SSM `billing-url`, `billing-client-id`, `billing-client-secret`, `billing-event-secret`).
The signed `invoice.paid` and payout events arrive at `POST /v1.0/internal/billing/events`; the cutover that put it live
is recorded in `docs/runbooks/billing-rail-cutover.md` (history; the rollback it describes no longer exists).

## 4. Inter (Pix): not the wallet's any more

The wallet no longer talks to Inter, holds no Inter secret and runs no gateway. The Pix gateway stack
(`CtechWallet-Prod-PixGateway`, two Lambdas and the mTLS webhook domain) was removed from the CDK and destroyed on
2026-10-15; its code is in git history before the P5 cleanup. The Inter connection, its keys and its webhook are Billing's
(`ctech-billing/docs/runbooks/tenant-zero-inter-rail.md`).

The SSM parameters `/ctech-wallet/{env}/inter/*` and `/ctech-wallet/{env}/pix-gateway/*` are no longer read. They were kept
for 30 days after the cleanup in case of a rollback; delete them after **2026-11-14**.

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

`cmd/reconcile` runs as a Lambda on EventBridge Scheduler every 5 min (`ReconcileStack`; it can also run as a CLI). It resolves
withdrawals stuck in `processing` (completes or reverses) and exits non-zero when a
reversal's credit-back fails (`refund_failed`) or a hold is stale. No CloudWatch alarm or SNS alert is wired to that exit, so
check the logs (`ALARM` lines) and the Lambda's error count.
It asks Billing about every processing payout, and re-reads aged pending PIX deposits
as a lost-event fallback. The Lambda role must include the PIX deposit table and its
status GSI, and the holds table status GSI for stale holds (an alarm, never an
automatic release). A lookup that errors leaves the withdrawal `processing`; it is
never read as "not found".
`cmd/reconcile` uses `config.LoadReconcile`; it does not require the API server's
JWT issuer, CORS, or fleet-wide Valkey settings.

## 5b. Turning on `REQUIRE_DESCRIPTION`

Default off. Turn on only after ctech-poker and ctech-billing are deployed with the description changes (check each
repo's latest release in its pipeline, not only in git). Add `REQUIRE_DESCRIPTION=true` to the static env written in
`cdk/lib/api-stack.ts` and redeploy: dev first, exercise a daily reward, a table buy-in
and cashout, a cosmetic purchase and a deposit, and watch the access log for
`400 description é obrigatório` for 24h before prod. Roll back by removing the line.

## 5a. Daily PIX limits (admin-only)

Per-wallet overrides are set directly in DynamoDB on the `real` wallet row (`wallets` table): `daily_deposit_cap`
(centavos, default 100000), `daily_withdraw_cap` (centavos, default 100000), `daily_withdraw_count` (default 1).
Absent or 0 means the default. Counters (`real_daily_counters`) live on the user row in `wallet_users`; to give a
user a fresh day by hand, remove that attribute. A paid deposit that exceeds the cap is refunded to the payer.

## 6. Gambling is Poker's

The wallet has no gambling switch, no activation, no limits and no self-exclusion screen: consent, personal limits and
self-exclusion live in Poker (`ctech-poker/docs/runbooks/poker-owns-games.md`), and the game wallet was merged into `real`.
Real money in Poker is still off in production. Before it is turned on, re-run Poker's `import-responsible` once more
(the wallet can no longer record a change, but the state recorded before the move is what the import reads), and check the
counts it prints against the wallet's own.

The wallet keeps one guard: `HoldGame` refuses a user whose self-exclusion is still recorded on the wallet's user row
(fail closed). It is the last line behind Poker's own check, for state an incomplete import could miss.

## 7. Account deletion (LGPD) participant

Plan: `docs/plans/2026-10-07-account-deletion-participant.md`. Contract: ctech-account
`docs/specs/2026-10-06-account-deletion-saga-protocol.md`.

**Prerequisites (in this order):**

1. ctech-account deployed with the deletion topic (SSM `/ctech/{env}/account/erasure-topic-arn` exists) and serving
   `cpf_hmac` on `GET /v1.0/internal/kyc/:user_id`.
2. `cdk deploy` the wallet: **DynamoDB first** and wait until the three new `gsi_user` indexes (deposits, withdrawals,
   holds) read `ACTIVE` (`aws dynamodb describe-table --table-name {env}_wallet_pix_deposits --query
   'Table.GlobalSecondaryIndexes[].IndexStatus'`; existing rows are indexed by DynamoDB, nothing to backfill), then
   `CtechWallet-{Env}-Erasure`, `-IAM`, `-API`. The API refuses to boot in prod without `ERASURE_QUEUE_URL`,
   `VALKEY_REVOCATION_URL` and the erasure client.
3. Dedicated erasure ack client seeded (§2a) and its SSM parameters set.
4. Publish the scope manifest (§1). It now includes `internal:wallet:erasure-eligibility`.
5. In ctech-account, add to `ERASURE_PARTICIPANTS`:
   `{"service":"wallet","url":"https://wallet-api.aoctech.app/v1.0","audience":"<value of /ctech-wallet/{env}/app-url>","client_id":"<erasure ack client id>"}`.
6. **Deployment check:** ctech-account mints the eligibility token with `iss` = its own issuer. Confirm that value
   equals the wallet's `CTECH_ISSUER_URL` (SSM `/ctech-account/{env}/app-url`). Then call
   `GET https://wallet-api.aoctech.app/v1.0/internal/erasure/eligibility/<test-sub>` with such a token and expect `200`;
   a `401` means the issuer or audience differ.
7. **IAM check in dev, before prod (R4):** with the dev API role, `REMOVE ip, user_agent` on a `wallet_audit` row must
   succeed and `SET event_type = :x` on the same row must fail with `AccessDeniedException`. If the first is denied,
   fix the IAM conditions before prod.

**What the purge does.** It moves no money and never touches the ledger.

- Payer CPF/name (deposits) and PIX keys (withdrawals) are copied to `{env}_wallet_erasure_retention`
  (pk `SUB#{sub}`, sk `{table}#{id}`, TTL 5 years), then removed from the rows.
- `wallet_users` loses limits, counters and self-exclusion. Consent versions stay.
- `wallet_audit` loses `ip`/`user_agent`.
- An active self-exclusion is kept as `wallet_users` `SELFEXCL#{cpf_hmac}` until it ends (indefinite: 5 years) and is
  re-applied if the same CPF activates gambling again.
- A `sandbox` balance is forfeited (virtual).

**Reading retained data (PLD/COAF request, legal).** The API role cannot read the retention table (IAM deny). Use
the console/CLI with an admin role, `--profile ctech`:
`aws dynamodb query --table-name prod_wallet_erasure_retention --key-condition-expression "pk = :p" --expression-attribute-values '{":p":{"S":"SUB#<sub>"}}'`.
Join with ctech-account's `KYCRET#{sub}` for the identity. Never copy results into tickets or logs.

**Messages in the erasure DLQ (`{env}-ctech-wallet-erasure-dlq`).** There is no CloudWatch alarm on it (alarms cost
money and were declined); check its depth when a user's deletion stays in `purging` in ctech-account.

- Read the message: `aws sqs receive-message --queue-url <dlq-url> --profile ctech`.
- Check the API logs for `erasure: message left for redelivery` with the same `message_id`.
- Typical causes: `cpf_hmac` missing (account not upgraded), the IAM audit condition (AccessDenied on
  `wallet_audit`), KYC endpoint down.
- Fix, then redrive: `aws sqs start-message-move-task --source-arn <dlq-arn> --profile ctech`.
- The purge is idempotent; redriving is always safe.

**Blocked ack after the point of no return (request `stalled` in ctech-account).** A blocker appeared after
LOCKED, typically a PIX deposit confirmed or a poker cash-out credited after the lock. The user cannot act
(no tokens).

- Support settles the money: PIX refund to the payer, or a payout to an account of the same CPF (the withdrawal
  flow cannot be used: the user has no tokens), with the usual reconciliation records.
- Confirm `GET /v1.0/internal/erasure/eligibility/<sub>` returns eligible.
- Redrive from ctech-account admin.

**Backup restore (PITR).** Restoring any wallet table can bring back erased PII. After a restore, list the requests
purged after the restore point (ctech-account deletion-requests table, see its runbook) and re-publish `user.erase`
for each. The purge reruns idempotently.

**Balance blocker.** It covers `real` (a retired `game` wallet, if any row still holds a balance, counts too). `real` leaves by Pix to an account of the same CPF. The daily withdrawal limits apply (default R$ 1.000 and one
withdrawal a day), so `detail.days_needed` can be more than one: the user must reach zero **before** requesting the
deletion, because the account is locked from confirmation. Ask support to raise the wallet's `daily_withdraw_cap` /
`daily_withdraw_count` (admin fields in DynamoDB) only after verifying the person.

**Deposits during and after the saga.** A deposit paid while the user is LOCKED credits and shows up as a balance
blocker. A deposit paid after the user is ERASED is refunded to the payer automatically (R18); check the refund
in the logs (`deposit for an erased user; refunding payer`) and the `ALARM` on failure. A paid charge with no payer
identity stays `pending` (quarantined) and blocks (`wallet.deposit_quarantined`): support attributes or refunds it.

### 7a. Personal-data export (LGPD art. 18, V)

The erasure queue also carries `user.export` from ctech-account (spec ctech-account `docs/specs/2026-10-17-data-export.md`;
`api-commons` ≥ v1.29.1, `dataexport.Handler` plugged into the consumer with `WithExporter` in `startErasureConsumer`).
`services.DataExporter` builds one zip, PUT to the presigned S3 URL in the message and acked to `/v1.0/internal/export/ack`.

- **Operator steps to enable:** grant the wallet's erasure-ack client the extra scope `internal:account:export-ack`
  (`createclient -grant`), then set `"export": true` on the wallet's entry in account's SSM `erasure-participants` and restart
  account. Until then account never asks the wallet.
- **Included:** `wallets.json`, `ledger-{type}.json` (the whole statement, paged to 100k entries), `deposits.json`,
  `withdrawals.json`, `holds.json`, `purchases-sandbox.json`, `purchases-product.json`, `settings.json` (terms, self-exclusion,
  limits, counters) and `audit.json` (latest 1000 events). Read-only: the ledger is append-only and the export is not an edit.
- **Counterparties are opaque** (spec E4): a transfer's other side is only a wallet id or the caller's reference. A deposit's payer name and CPF are exported only when the
  deposit is `confirmed` (the CPF check passed); on any other status they may be a third party's and are removed. Idempotency keys and request hashes
  never leave.
- `TestEveryTableIsClassifiedForTheExport` reads the CDK table names: a new table must be exported or excluded with a reason, **next
  to its erasure treatment**. The four retired `wallet_*` tables (baas, transfer intents, settlement legs, MED receivables) are
  excluded as no longer written; they are not in the purge either, so confirm they hold no person data before deleting them.
