# Asaas BaaS custody — implementation plan

**Date:** 2026-07-30
**Supersedes:** nothing — operationalizes `docs/specs/2026-07-29-asaas-baas-custody-design.md` (the design) and
incorporates `docs/specs/2026-07-30-legal-audit-asaas-baas-gambling.md` (the legal audit) into a build sequence.
**Status:** build-only. **Production launch of the Asaas custody path and of real-money games stays blocked** —
see §0. Everything in this plan ships behind feature flags, dark, fully integration-tested against Asaas's
sandbox environment and DynamoDB-local, so the moment legal sign-off lands there is no engineering left to do.
**New scope added in this revision:** §9, a decoupled PIX→sandbox direct-purchase flow, requested independently of
the custody migration. This revision also replaces §3.3's KMS-based key storage with local AES-GCM keyed by a
single SSM SecureString, corrects §3.2's EVP rate-limit scope assumption, and reroutes §9's charge/refund
mechanics from Asaas's root account to the existing Inter integration. §9.1a is new: the pre-existing
`game → sandbox` purchase (`PurchaseSandbox`) gains a real Asaas settlement leg, since it moves real custodied
money and was otherwise left as a ledger-only op that would drift from Asaas's system of record.

Read `../../CLAUDE.md` (root) and `../../api/CLAUDE.md` before touching any of this — the Financial Safety
Invariants are non-negotiable, and this plan does not weaken any of the twelve. It adds three new ones (#13–#15,
already named in the design spec §7) and documents why the new §9 feature does not need a sixteenth.

---

## 0. What "blocked" means here, precisely

Two independent gates, tracked separately because they resolve on different timelines and different people:

| Gate | Blocks | Resolved by |
|------|--------|-------------|
| **Custody gate** | Any real user's `real` wallet being backed by an Asaas subaccount in production | Confirmation that the Asaas–CTech BaaS contract conforms to Res. Conj. 16/2025 (deadline 31/12/2026, audit §8 item 8) + the mandate/closure/MED contract text (spec §9.2, §9.9) is signed and shown to users |
| **Games gate** | `GAMBLING_ENABLED=true` in production, i.e. any real-money poker/dominó table | Signed counsel opinion on habitualidade/classification (skill vs. chance, DL 3.688/1941 art. 50), on CC art. 814 caput enforceability, and — per the audit's new finding — awareness that Lei 15.358/2026 art. 21-A can force Asaas to freeze already-custodied balances retroactively if SPA later disagrees |

Both gates are `false` by default in every environment except a dedicated Asaas-sandbox integration-test
environment. Nothing in this plan asks anyone to flip either gate. The work here is: build the custody layer,
the settlement layer, the new failure-mode handlers (frozen subaccount, closure, MED clawback, transfer
authorization), and the decoupled sandbox-purchase flow, all dark, all tested end-to-end against Asaas's real
sandbox API — so flipping the gate later is an ops decision, not an engineering one.

The **existing Inter integration keeps running in production, unchanged, for the whole build.** `pix` (Inter) and
the new `asaas` package coexist; nothing here touches `internal/pix/*`, `services/wallet.go`'s existing
Inter-backed methods, or `GAMBLING_ENABLED`'s current meaning. See §10 for exactly when Inter is retired (not in
this plan — separate migration spec per the design doc's non-goals, §12).

Once the custody gate clears and `real`-wallet deposits/withdrawals move onto Asaas subaccounts, Inter's only
remaining role in this codebase is the sandbox-purchase rail (§9) — CTech's own revenue, not custody, which is
exactly the shape Inter's pooled-account model is still right for (custody is what forced the move off it, not
holding CTech's own money). §9 is built against Inter from day one for this reason, not as a stopgap.

---

## 1. New feature flags (all default `false`)

Added to `api/internal/config/config.go`, same pattern as `GamblingEnabled`:

```go
// AsaasCustodyEnabled gates the entire Asaas onboarding/deposit/withdrawal/
// settlement surface. With it off, a user's real wallet is created and backed
// exactly as today (Inter PJ pooled account) — no behavior change. Turning it on
// requires the custody gate (§0) to be cleared; nothing in code enforces that
// legal precondition, so do not flip it in prod without it.
AsaasCustodyEnabled bool `env:"ASAAS_CUSTODY_ENABLED" envDefault:"false"`

// AsaasWithdrawalFeeEnabled gates charging the 2% withdrawal fee on Asaas-custodied
// wallets. Off because the fee is a live legal blocker independent of custody
// (Res. Conj. 16/2025 art. 8º XI substance-over-form risk — design spec §6,
// audit §8). Withdrawals stay fee-free while this is false, same as the
// real↔game edge already is.
AsaasWithdrawalFeeEnabled bool `env:"ASAAS_WITHDRAWAL_FEE_ENABLED" envDefault:"false"`
```

The PIX→sandbox purchase flow (§9) is **not** behind a flag. Its one open legal question (CDC art. 49 refund
treatment for unused paid credits, design spec §9.9 item b) is resolved in §9.2 below with a verifiable
eligibility rule, so there is nothing left gating it — it ships live once built and tested.

`GamblingEnabled` is untouched — it still gates real-money play, independent of which custody backend `real` sits
on. A future state has `AsaasCustodyEnabled=true` and `GamblingEnabled=false` (custody live, games still blocked):
that is in fact the intended state at the end of design-spec phase 1 (§11).

---

## 2. Architecture additions

### 2.1 New package `internal/asaas`, parallel to `internal/pix`

Mirrors the existing pattern exactly (`client.go` interface + `fake.go` for unit tests + a real implementation),
because `internal/pix` already proves the shape works for this codebase and DRY says reuse it, not invent a new
one:

```
internal/asaas/
├── client.go        # AsaasClient interface — accounts, PIX charges/QR, transfers, documents, webhooks
├── fake.go           # in-memory fake for unit tests (mirrors pix/fake.go)
├── lambda_client.go  # real implementation — invokes pix-gateway's outbound Lambda (extended, see 2.2)
└── model.go          # Account, Charge, Transfer, TransferAuthorization, WebhookEvent types
```

```go
// AsaasClient is the surface the wallet service depends on. One implementation
// per environment (fake for tests, Lambda-backed for real), same DI shape as
// pix.PixClient.
type AsaasClient interface {
    CreateAccount(ctx context.Context, req CreateAccountRequest) (*Account, error)
    UploadDocument(ctx context.Context, subaccountAPIKey, documentID string, file []byte) error
    CreateStaticPixKey(ctx context.Context, subaccountAPIKey string) (*PixAddressKey, error)

    CreatePixQRCode(ctx context.Context, subaccountAPIKey string, req QRCodeRequest) (*QRCode, error)
    QueryPayment(ctx context.Context, apiKey, paymentID string) (*Payment, error)

    CreateTransfer(ctx context.Context, apiKey string, req TransferRequest) (*Transfer, error)
    QueryTransfer(ctx context.Context, apiKey, transferID string) (*Transfer, error)
}
```

`apiKey` is passed per call (never stored as client state) because every subaccount authenticates with its own
key, and the parent uses its own — a single `AsaasClient` instance is safe to share across all users.

No `CreateCharge`/`RefundCharge` on this interface: §9's sandbox-purchase flow moved to the existing Inter
integration (see §9.1), so Asaas never needs a root-account charge/refund path. Don't add these back
speculatively — if this ever changes, add them then.

### 2.2 Extend `pix-gateway`'s outbound Lambda — do not build a second egress path

The design spec is explicit that `pix-gateway` survives *because* `api.asaas.com` has no AAAA record and `api`'s
ALB is IPv6-only (§3.1). That means the fix belongs in `pix-gateway`'s `cmd/outbound`, not in `api`: add an
action/route family for Asaas (`asaas.CreateAccount`, `asaas.CreateTransfer`, etc.) alongside the existing
Inter actions, reusing the same Lambda, the same IPv4 egress, the same invoke contract shape `api` already calls
via `PixGatewayFunctionName`. `internal/asaas/lambda_client.go` invokes the *same* Lambda function
(`cfg.PixGatewayFunctionName` — no new env var needed) with a different action discriminator in the payload.

This is cross-project (`pix-gateway` is presumably a sibling repo/service — confirm its location before starting;
not verified in this session). **Flag this explicitly to whoever owns `pix-gateway`: this plan assumes its
outbound Lambda is extended, not replaced.** If `pix-gateway` turns out to be structured such that adding Asaas
actions is awkward, that's a real design conversation, not a decision to make unilaterally here.

### 2.3 Webhook ingress does NOT need the mTLS Trust Store — but needs a NEW synchronous piece the design spec never described

Per the design spec's "what dies" table (§3.2), Asaas authenticates webhooks with a header token
(`asaas-access-token`, or whatever name we configure at webhook creation), not mTLS — so the inbound Asaas webhook
can land directly on `api`'s existing IPv6 ALB via a plain route, e.g. `POST /internal/asaas/webhook`, no API
Gateway Trust Store, no `pix.wallet.aoctech.app` custom domain. Simpler than Inter's inbound path, not harder.

**What the design spec does not cover, and this plan must add as first-class architecture:** every
`POST /v3/transfers` call — a withdrawal payout, a fee sweep, a settlement leg — triggers Asaas to call back a
**separate, synchronous "transfer authorization" webhook** roughly 5 seconds later, and the wallet must respond
`{"status":"APPROVED"}` or `{"status":"REFUSED","refuseReason":"..."}`. Failing to answer validly up to 3 times
**auto-cancels the transfer**. This sits in the critical path of every money-out operation Asaas performs on our
behalf, and it is a new endpoint the spec never named:

```
POST /internal/asaas/transfer-authorization        (header: asaas-access-token)
```

Design:

1. Every place that calls `AsaasClient.CreateTransfer` first writes a `wallet_transfer_intents` row (see §3.5),
   keyed by the same `externalReference` it passes to `CreateTransfer`, recording what the transfer *should* be
   (amount, destination type, the withdrawal/settlement/closure ID it belongs to) and setting
   `status=awaiting_authorization`.
2. The authorization webhook handler does **one DynamoDB `GetItem` by `externalReference`, no outbound calls**
   (latency budget is tight — 3 failed responses cancel the transfer), compares the payload's amount/destination
   against the stored intent, and returns `APPROVED` on a match, `REFUSED` on any mismatch or unknown reference.
3. This is the single choke point that would catch, e.g., a bug that tried to transfer to a `pixAddressKey` that
   isn't the withdrawing user's own verified CPF — belt-and-suspenders on top of the application-level check that
   already builds the transfer request correctly.

This is new work, not a reinterpretation of anything in the design spec — call it out explicitly in code review
and in the spec amendment (§11).

### 2.4 New DynamoDB tables (constants added to `domain/wallet/model.go`, same pattern as existing `Table*`)

```go
const (
    TableBaasAccounts     = "wallet_baas_accounts"     // 1 row per user, custody lifecycle state
    TableTransferIntents  = "wallet_transfer_intents"  // §2.3 — transfer-authorization lookup
    TableSettlementLegs   = "wallet_settlement_legs"   // §5.4 netting batches
    TableMedReceivables   = "wallet_med_receivables"   // §7 clawback debt
    TableSandboxPurchases = "wallet_sandbox_purchases" // §9 — decoupled from wallet ledger tables on purpose
)

const (
    GSIBaasAccountID = "gsi_baas_account_id" // wallet_baas_accounts.provider_account_id → webhook resolution
    GSIBatchStatus   = "gsi_batch_status"    // wallet_settlement_legs.status → drift/convergence scan
    GSIMedStatus     = "gsi_med_status"      // wallet_med_receivables.status → open-debt scan, blocks withdrawal
)
```

Table/field names are provider-neutral on purpose — Asaas is today's provider, not a permanent commitment, and
nothing in the schema should have to be renamed if it's ever swapped. Only the `internal/asaas` adapter package
and its `AsaasClient` calls stay Asaas-named, since that's exactly the piece a provider swap would replace.

`wallet_baas_accounts` (PK `user_id`) is deliberately its own table, not a new field bag on `wallets` — the
`real`/`game`/`sandbox` rows in `wallets` stay exactly as they are today (design spec §3.1: "the entire ledger core
survives... none of it changes"). Custody lifecycle (`onboarding → pending_documents → pending_approval →
approved → frozen → closing → subaccount_closed → closed`) lives entirely in the new table; `GetBalances` checks
it before deciding whether to call `EnsureRealWallet` at all (§4.1).

---

## 3. Onboarding (design spec §5.1)

### 3.1 `ctech-account` change — the one field gap

`kycclient.KYC` (`api/internal/kycclient/kycclient.go:24`) currently returns `Level, CPF, LegalName, BirthDate`.
Extend `ctech-account`'s internal KYC endpoint to also return email, phone, and address (street/number/complement/
district/city/state/zip) — no new KYC field, no new form, per design spec §5.1.1. This is a `ctech-account`
PR, cross-reviewed here but implemented there.

`incomeValue` (an Asaas cadastral field, not an identity attribute) is collected by the wallet's own real-wallet
activation step, sent to Asaas, and **not persisted** — per spec §5.1.1, absent a documented retention need under
LGPD arts. 6º/9º/18. Add the field to the onboarding request DTO with a comment stating exactly this, so nobody
"helpfully" adds a database column for it later.

### 3.2 `POST /v1.0/wallet/onboarding`

New route, new service method `InitiateBaasOnboarding(ctx, userID) (*BaasAccount, error)`:

1. Require `kycLevel == verified` (mirrors `ActivateGambling`'s gate).
2. Idempotent on `user_id` — a second call while `status` is anything other than absent returns the existing
   record (mirrors `EnsureRealWallet`'s create-or-reuse pattern, never a second `POST /v3/accounts`).
3. `asaas.CreateAccount` with the parent API key; on success, write the `wallet_baas_accounts` row with
   `status=onboarding`, KMS-encrypted `api_key_ciphertext` (§3.3), `provider_account_id`, `provider_wallet_id`
   (Asaas's `account.id`/`walletId` — named generically because a future provider's IDs land in the same columns).
4. Audit event `EventBaasSubaccountCreated` (new constant in `domain/wallet/audit.go`, same append-only table,
   same pattern as `EventGamblingActivated`).
5. **Do not call `EnsureRealWallet` here.** Per design spec §5.1: "A `real` wallet may not exist before a custody
   account exists to back it — otherwise Invariant #13 is false from birth." `GetBalances` (§4.1) is the one place
   that decides whether a `real` wallet exists yet.
6. Poll (or webhook-driven, per Asaas's `onboardingUrl` branch in spec §5.1 step 3) until
   `ACCOUNT_STATUS_APPROVED` → create the static EVP PIX key **once, ever, per subaccount** (never re-derived —
   cached from here on, same posture as the `qr_code_id → txid` mapping in §4.2) → **now** call `EnsureRealWallet`
   → audit `EventWalletActivated`.

   The BCB EVP-key creation rate limit (design spec §5.1 step 5: "1 creation/minute") is read here as **per
   subaccount**, not fleet-wide across every Asaas client under CTech's parent account — confirm this against
   Asaas's actual docs/support before build (same "verify before hardcoding" posture as the `transferFee` field
   name in §5.2). Read this way it's a non-problem in practice: each subaccount calls `POST /v3/pix/addressKeys`
   exactly once in its lifetime, so a plain retry-with-backoff on a 429 is enough — no fleet-wide queue or
   rate-limited worker needed. Only build that machinery if the limit turns out to be fleet-wide after all; don't
   build it speculatively.

### 3.3 API key storage — AES-GCM locally, master key from SSM SecureString

Design spec §5.1.2 rejects SSM SecureString for a different architecture than the one below: it evaluates storing
each user's *ciphertext* as its own SSM parameter (1 per user → hits the 10k-parameter regional ceiling around
10k users) and lands on KMS envelope encryption instead. That comparison doesn't apply here — this plan stores
exactly **one** SSM SecureString for the whole fleet, the AES-256 master key, never one per user. The per-user
ciphertext stays exactly where the design spec put it, in the `wallet_baas_accounts` item; only the encryption
mechanism underneath changes.

- One AES-256 key, generated once, stored as a single SSM SecureString — same pattern this codebase already uses
  for the Inter mTLS cert/key and client secret (`start.sh` reads secrets from SSM at boot; `internal/awsclient`
  already wires an `ssm.Client` for exactly this). Path: `/ctech-wallet/{env}/asaas/api-key-master`, mirroring
  `iam-stack.ts`'s existing `ssm:GetParameter` scoping for the Inter secrets.
- Fetched exactly **once**, at server startup (`cmd/server/main.go`'s fx bootstrap), cached in memory for the
  process lifetime — never re-fetched per request, never re-fetched per encrypt/decrypt call.
- Encrypt/decrypt with stdlib `crypto/aes` + `crypto/cipher` (AES-256-GCM, random nonce per encryption, nonce
  stored alongside the ciphertext) — no new dependency, no network call in the hot path. `api_key_ciphertext`
  keeps the same shape/location as the design spec's KMS version; only what produces/consumes it changes.
- Why this beats KMS here: a KMS `Decrypt` call is a network round-trip (~10-20ms, plus per-request cost) on
  every code path that needs a subaccount's API key — deposit, withdrawal, closure, every settlement leg. Local
  AES-GCM is single-digit microseconds and free after the one-time SSM fetch. No CMK to provision, no
  `kms:Decrypt` IAM policy, no per-request KMS cost line.
- Key rotation: write a new SSM value, re-encrypt all `api_key_ciphertext` rows under the new key in a background
  migration, retire the old value only once that migration completes — same operational shape as rotating the
  Inter mTLS secret, already documented in `OPERATIONS.md`, not a new rotation story.
- This needs a **cdk** change (new SSM SecureString parameter, `ssm:GetParameter` IAM scoping to the `api` role
  on that one path) — flag to whoever owns `cdk/`; smaller diff than the KMS CMK it replaces.

### 3.4 Testing

- Unit: `AsaasClient` fake simulates `CreateAccount` success/failure, `ACCOUNT_STATUS_APPROVED` webhook, EVP key
  creation including the 1/minute BCB rate limit (fake returns a rate-limited error on the second call within a
  window; the retry/backoff path is what's under test, not the real limiter).
- Integration (DynamoDB-local): full onboarding state machine, idempotent replay at every step, AES-GCM
  encrypt/decrypt round-trip against a fixed test master key (same convention as other test secrets in
  `tests/integration` — no LocalStack/KMS/SSM dependency needed here, the crypto itself is pure Go stdlib).
- **Asaas-sandbox integration test** (new test tag, e.g. `//go:build asaas_sandbox`, run manually / in a dedicated
  CI job, not in the default `make test-integration`): real `POST /v3/accounts` against `api-sandbox.asaas.com`,
  real document upload, real webhook round-trip. This is the only way to catch drift against Asaas's actual API
  before it hits anyone's money.

---

## 4. Deposit (design spec §5.2)

### 4.1 `GetBalances` gains an onboarding-state branch

```go
func (s *WalletService) GetBalances(ctx context.Context, userID string) (real, game, sandbox *wallet.Wallet, custodyStatus string, err error)
```

When `AsaasCustodyEnabled`: read `wallet_baas_accounts`; if absent or `status != approved`, return
`custodyStatus` (`"onboarding"`, `"pending_documents"`, etc.) and **do not** call `EnsureRealWallet`. The UI reads
this to show the right onboarding step instead of a balance card. When `AsaasCustodyEnabled=false` (today, and for
the whole build phase unless explicitly testing), behavior is byte-for-byte what it is now — this is why the flag
default is safe.

### 4.2 `InitiateDeposit` — same gates, new backend

Unchanged: `kycLevel`, `MaxInboundAmount`, `ValidateDepositAmount` (checked before any charge is opened, same as
today — Invariant 11's spirit). New gate: subaccount `status == approved`, else `409 wallet-onboarding` (new
problem type, §8). Then:

```go
qr, err := s.asaas.CreatePixQRCode(ctx, subaccountAPIKey, asaas.QRCodeRequest{
    AddressKey: cachedEVPKey, Value: amount, Format: "ALL",
    ExpirationSeconds: 900, AllowsMultiplePayments: false,
    ExternalReference: txid,
})
```

`PutDeposit` persists `provider_qr_code_id` alongside `txid` — this mapping is required (design spec §5.2 step 3
flags it explicitly) because the webhook resolves `payment.pixQrCodeId → txid`, not the other way round.

### 4.3 `ConfirmDeposit` — same anti-spoofing shape, different transport

The webhook (`PAYMENT_RECEIVED`, header `asaas-access-token`) is a wake-up signal only, exactly like today's
Inter integration (Invariant 11). Resolve `account.id → user` (via `GSIBaasAccountID`), resolve
`payment.pixQrCodeId → txid`, then call the **existing** `ConfirmDeposit(ctx, txid, payerCPF, payerName, sweep)`
— this function does not need to change at all; only what feeds it does. `asaas.QueryPayment` replaces
`pix.QueryCharge` as the source of truth, called with the *subaccount's* API key.

One real gap to close, not present in the Inter integration: confirm whether Asaas's payment-query response
exposes the actual Pix payer's CPF, or only a merchant-side `customer` record (design spec §10 Q15 — flagged open,
and the research for this plan corroborated it: `payment.customer` is a record Asaas lets *us* create, not a
statement of who actually paid). If it does not expose payer CPF/name on query, **the CPF anti-fraud gate must
come entirely from the webhook body**, same as Inter today (`UpdateDepositPayer` is the only source there too) —
confirm this with a live Asaas-sandbox test before writing the gate logic, don't assume either way.

### 4.4 Testing

Same integration-test shape as the existing `tests/integration` deposit tests (webhook → re-query → credit,
CPF mismatch → refund, double-payment → excess refund, sweep path skips the CPF gate) — literally the same test
cases, pointed at the `asaas` fake instead of the `pix` fake. This is the value of not touching `ConfirmDeposit`'s
signature/logic: the existing test suite's *scenarios* transfer over almost unchanged; only the setup transport
differs.

---

## 5. Withdrawal (design spec §6, §6.4)

### 5.1 Fee stays off; the mechanics change underneath it

`AsaasWithdrawalFeeEnabled=false` for the whole build phase (§0 games-gate-independent legal blocker). Build the
two-leg transfer mechanics now so flipping the flag later is instant, but until it's on, withdrawals debit
`amount` only (fee=0), same visible behavior as today.

### 5.2 Two-leg transfer, only relevant once the fee flag is on

```
leg 1  asaas.CreateTransfer(subaccountAPIKey, {Value: amount, PixAddressKey: kyc.CPF, PixAddressKeyType: "CPF",
                             ExternalReference: withdrawalID + "#payout"})
       → wait for status DONE (poll via reconcile, same shape as today's processing-withdrawal sweep)
leg 2  asaas.CreateTransfer(subaccountAPIKey, {Value: fee - leg1Response.TransferFee, WalletID: parentWalletID,
                             ExternalReference: withdrawalID + "#fee"})
       → fires only AFTER leg 1 reaches DONE; non-fatal on failure (reconcile retries the sweep)
```

Leg 2's amount is `fee − T`, where `T` is leg 1's own `transferFee` (confirm this exact field name against a live
Asaas-sandbox response before hardcoding it — the research for this plan found it referenced but not
independently verified). Sweeping the fee before leg 1 completes is explicitly wrong (design spec §6.4.2): it can
starve the payout, and `T` isn't known until leg 1 responds.

Two carve-outs, both required before the fee flag can ever go on, both testable now:

- **Closure payout is always fee-free, CTech-funded if the balance is smaller than Asaas's own transfer cost**
  (§6). Otherwise dust traps money and the closure flow (§6 below) can never complete — this is not a nice-to-have,
  it's a precondition for `POST /wallet/closure` to terminate at all.
- **A full-balance withdrawal is never rejected by `min_withdrawal`** — otherwise a user under the minimum can
  never reach their own money without going through closure, which is a worse experience and a bigger dust trap.

Both carve-outs are pure `WithdrawalFee`/`WithdrawalAmount` logic changes in `domain/wallet/fee.go` — write the
unit tests (min/max boundary, full-balance carve-out, closure-fee-free path) now, before the fee flag exists to
flip, exactly as `fee_test.go` already tests the current 2%/min/max clamp.

### 5.3 Testing

- Unit: `fee.go` boundary tests for the two new carve-outs (§5.2).
- Integration: two-leg transfer with leg 1 success/leg 2 failure (leg 2 must be retried, never blocks the payout
  that already landed), leg 1 failure (existing `processing` reconciliation path, unchanged), transfer-authorization
  webhook approving/refusing each leg based on the stored intent (§2.3).
- Chaos-style test: authorization webhook never arrives (3 attempts exhausted, Asaas auto-cancels) — the
  withdrawal must land in `processing` and the reconcile job's `QueryTransfer` must detect the cancellation and
  reverse the debit (Invariant #12 — no money left in limbo, same contract as today's Inter failure path).

---

## 6. Settlement / netting (design spec §5.4.3–5.4.4)

Real-money poker settlement between subaccounts (once the games gate opens — build and test now regardless):

- `batch_id` (ULID) per settlement event; each leg's `externalReference = <batch_id>#<leg_n>`.
- Before ever sending a leg, `GET /v3/transfers?externalReference=...` — idempotent by construction, matching
  Invariant #3.
- Convergence window is bounded (minutes, not days) — a background job (extend `cmd/reconcile` or add
  `cmd/settle`, following the same Lambda/CLI dual-mode pattern already in `cmd/reconcile/main.go`) drives legs to
  `DONE` and alarms on anything still pending past the window.
- **Invariant #13 conservation check**: `Σ subaccount balances == Σ(real + game + open holds)` at quiescence.
  Unexplained drift **fails closed** — blocks new withdrawals and new game holds (a new kill-switch condition
  checked in `HoldGame`/`Withdraw`, not a separate flag) until reconciled. This is the load-bearing new invariant;
  give it its own alarm and its own runbook entry, not a generic log line.
- Residual risk named explicitly in the design spec (§5.4.4): a counterparty subaccount frozen mid-settlement.
  CTech sizes an **operational reserve** for this — it is not fully outsourced to the winner's patience. Track
  the reserve sizing as an ops/finance follow-up, not an engineering task, but the code must expose the number
  (open settlement exposure per user) so finance can size it.

### Testing

Integration: multi-leg batch with one leg's counterparty subaccount frozen mid-batch (simulated via the fake's
`ACCOUNT_STATUS_*` webhook injection) — verify the conservation check fires, withdrawals block, and the batch
resumes cleanly once the freeze lifts. This is the single highest-value test in this whole plan; do not skip it
for time.

---

## 7. New failure-mode handlers the migration itself requires

These exist because moving custody off a single pooled account introduces failure modes that never existed
before — none of them are optional polish, each is named in the design spec as a launch blocker for the custody
gate (not just the games gate).

### 7.1 Frozen subaccount (design spec §5.5)

New wallet-lifecycle state `frozen` in `wallet_baas_accounts.status`, driven by Asaas's balance-block webhook
category. New problem type `account-blocked` (§8). Every money-out path (`Withdraw`, `ringTransfer`,
`CashoutGame`) checks this status before acting — silently failing here is explicitly Invariant #12 territory
(no money left in limbo just because the wallet happens to be frozen instead of merely busy).

Note the audit's new finding directly feeds this: Lei 15.358/2026 art. 21-A can force this freeze *by statute*,
not by Asaas's own risk decision — the handler must not assume "frozen" always means a temporary AML hold; the
UI copy and the ops runbook both need a branch for "regulatory block, do not expect this to self-resolve."

### 7.2 Closure / revocation (design spec §5.6) — does not exist today, is a launch blocker

New route `POST /v1.0/wallet/closure` (user JWT + `RequireRecentMFA` + `Idempotency-Key`, same step-up shape as
withdrawal). Not a single request — a state machine driven by the same reconciliation job pattern as §5.3:

```
requested → (refuse 409 if open settlement exists — settle first) → closing → paid_out → subaccount_closed → closed
```

Payout is the fee-free/CTech-funded carve-out from §5.2 — this is *why* that carve-out is a precondition, not an
enhancement. Mandate extinction on death/incapacity (CC art. 682, II) is the same clause already drafted in
addendum v2.2 clause 7 ("extingue-se automaticamente pela morte") — no new legal text needed here, just the code
path that acts on it when `ctech-account` reports a user as deceased/incapacitated (confirm that signal exists;
not verified in this session — if it doesn't, that's a `ctech-account` gap to raise, not something this service
can infer on its own).

### 7.3 MED clawback (design spec §5.7)

Asaas can reverse a credited Pix up to 80 days later (Mecanismo Especial de Devolução). By then the money may be
spent. Required:

1. Detect via a **dedicated** Asaas webhook event for MED (not via the balance-conservation check discovering a
   mismatch after the fact — that's a symptom, not a signal).
2. Debit what's currently there; any shortfall becomes an explicit `wallet_med_receivables` row — **never** a
   negative balance (Invariant #1 stays literal: the balance itself never goes below zero; the receivable is a
   separate debt record).
3. Block funding/withdrawal on that wallet while a receivable is open; settle it from the next inflow.
4. `wallet_med_receivables` rows are named explicitly and excluded from the Invariant #13 conservation check's
   "everything is explained" set on their own line — a receivable is not custody drift, and conflating the two
   would hide a real drift behind a legitimate receivable or vice versa.

### 7.4 Testing (7.1–7.3 together)

Integration: frozen-subaccount withdrawal attempt → `409 account-blocked`, never `500`, never silent. Closure
happy path + closure-while-settlement-open (refused, then retried after settlement) + closure with
balance-below-transfer-cost (CTech-funded leg fires). MED clawback with sufficient balance (clean debit) and with
insufficient balance (receivable created, withdrawal blocked, later inflow settles it automatically).

---

## 8. New problem types

Added to `internal/problem/problem.go`, same pattern as the existing wallet-specific block:

```go
const (
    TypeWalletOnboarding = "/problems/wallet-onboarding"
    TypeAccountBlocked   = "/problems/account-blocked"
    TypeMedReceivableOpen = "/problems/med-receivable-open"
    TypeSandboxPurchaseUsed = "/problems/sandbox-purchase-used"
)

func WalletOnboarding(status string) *Problem { /* 409, carries current custody status */ }
func AccountBlocked() *Problem                 { /* 409 */ }
func MedReceivableOpen() *Problem              { /* 409 */ }
func SandboxPurchaseUsed() *Problem            { /* 409 — §9.2 eligibility check failed */ }
```

---

## 9. New, decoupled feature: direct PIX→sandbox purchase

### 9.1 What this is, and — more importantly — what it is not

This is a **product sale**, not a wallet transfer. A user pays CTech, via PIX, for a pack of sandbox credits
(e.g. "R$ 1,00 → 100.000 fichas"), the same way they'd pay for in-game currency in any mobile game.

**This flow uses the existing Inter integration (`internal/pix`), not Asaas.** Asaas subaccounts exist to hold
*user* money under custody (Res. Conj. 16/2025) — this money is never the user's, it's CTech's revenue from the
instant the charge confirms, and it lands directly in CTech's own pooled Inter account exactly the way
`real`-wallet deposits do today. Building a second charge/refund mechanism against Asaas's root account for this
would duplicate a working, already-integrated, already-tested PIX rail for no benefit — reuse
`pix.CreateCharge`/`pix.QueryCharge`/`pix.Refund` as-is (§9.3), the same interface `InitiateDeposit`/
`ConfirmDeposit` already call. This also keeps the new Asaas/`pix-gateway` action set (§2.2) scoped entirely to
real custody operations — sandbox commerce keeps running on the Inter rail that's already in production,
unaffected by whatever state the Asaas integration is in.

The money never touches the `real` or `game` ledger at any point. This is why it does not need to route through
`FundGame`/`PurchaseSandbox` at all, and why it is legitimate to expose it with **no KYC gate**, per your own
framing: this money is not held on the user's behalf pending a future obligation (that would be custody, and
custody is exactly what triggers Res. Conj. 16/2025 and the KYC/AML apparatus built around it) — it is
consideration for a completed sale of a non-monetary digital good, settled the instant the charge confirms.

**Why this does not weaken Invariant #7** ("real money enters the ring-fence ONLY via `real → game`; there is no
`real → sandbox` path"): that invariant protects the *metering point for gambling exposure* — the one edge the
personal-limit engine watches so a cap can't be bypassed. Sandbox credits bought this way carry exactly the same
properties sandbox always has: no monetary value, never convertible back to `game` or `real`, never withdrawable.
Nothing about this flow lets a user convert real gambling exposure into anything, because sandbox was never part
of that exposure to begin with — it's a pure entertainment purchase, same category as buying a skin. The ledger
line `real → game → sandbox` is untouched and still the only path by which *ring-fenced gambling money* becomes
sandbox; this feature adds a second, entirely separate source of sandbox credits that was never gambling money in
the first place. **This must be reflected in code, not just in this paragraph:** the purchase path never calls
`ringTransfer`, never touches `wallets` rows for `real`/`game`, and is excluded from the Invariant #13 conservation
check exactly as `sandbox` already is (design spec §3: "`sandbox` is excluded because it is virtual").

### 9.1a `game → sandbox` purchase (`PurchaseSandbox`) needs a real Asaas settlement leg — resolved

`PurchaseSandbox` (`internal/services/wallet.go:780`) predates this plan and today calls `ringTransfer`
(`wallet.go:681`) — a pure DynamoDB `TransactWriteItems` ledger op: debit `game`, credit `sandbox`, done. Correct
**today**, because `real`/`game`/`sandbox` are just counters over one pooled Inter account. Once a user is
Asaas-activated, `game`'s balance is Invariant #9 real money sitting in *that user's* Asaas subaccount, and
spending it on sandbox credits is the same kind of sale §9.1 establishes: the money stops being the user's and
becomes CTech's revenue the instant the purchase lands. Left as ledger-only, the ledger would say the money left
`game` while Asaas's system of record still shows it in the user's subaccount — manufacturing the exact drift
§6's Invariant #13 conservation check exists to catch, on every single game-funded purchase.

**Design: `PurchaseSandbox` gains a conditional settlement leg, reusing §2.3's transfer-intent/authorization
machinery and §6's reconcile job — no new transfer mechanism, no new table.**

```go
// PurchaseSandbox converts game money into sandbox credits (game → sandbox).
// ... [existing godoc unchanged] ...
//
// Once the caller's game wallet is Asaas-custodied (wallet_baas_accounts.status
// == approved), this also settles the real money externally: subaccount →
// CTech's Asaas master account, via the same wallet_transfer_intents /
// transfer-authorization / reconcile machinery as every other CreateTransfer
// call in this plan (§2.3, §5.2, §6). Pre-custody (or for a non-activated
// pool-account user) it stays exactly as it is today: ledger-only, no external
// call — same conditional branch shape as GetBalances' onboarding-state check
// (§4.1).
func (s *WalletService) PurchaseSandbox(ctx context.Context, userID string, amount int64, idemKey string) (debit, credit *wallet.LedgerEntry, err error) {
    _, game, sandbox, err := s.requireActivated(ctx, userID)
    if err != nil {
        return nil, nil, err
    }
    credits := wallet.ToSandboxCredits(amount)
    debit, credit, err = s.ringTransfer(ctx, game, sandbox, amount, credits,
        wallet.EntrySandboxPurchase, wallet.EntrySandboxCredit, "sandbox_purchase", idemKey,
    )
    if err != nil {
        return nil, nil, err
    }
    // Ledger truth committed above (balance already proven by ringTransfer's
    // conditional write) — the external settlement leg follows, same ordering
    // as every other ledger-then-external-call flow in this codebase (Withdraw,
    // §9.2's refund). Non-fatal on failure to submit: reconcile retries.
    if baas, err := s.baas.GetIfApproved(ctx, userID); err == nil && baas != nil {
        s.settleGamePurchaseExternally(ctx, baas, credit.SK, amount, idemKey) // logs+alarms internally, never fails the purchase
    }
    return debit, credit, nil
}
```

- `settleGamePurchaseExternally` writes a `wallet_transfer_intents` row (§2.3) — same table, new `kind` value
  `sandbox_purchase_settlement` alongside the existing withdrawal-payout/fee-sweep/settlement-leg kinds —
  `externalReference = "sbxg#" + idemKey`, `status=awaiting_authorization`, `amount`, `from=subaccountAPIKey`,
  `to=parentWalletID`, plus `ledger_credit_sk` (see reversal below). Then calls
  `asaas.CreateTransfer(ctx, baas.SubaccountAPIKey, {Value: amount, WalletID: parentWalletID,
  ExternalReference: externalReference})` synchronously (no goroutine — request handler, per `api/CLAUDE.md`'s Go
  rules). If the call itself fails to submit, log + alarm and leave the intent row `awaiting_authorization`; do
  **not** unwind the already-committed ledger transfer (the sandbox credits are real and granted — this failure
  is a custody-accounting problem to reconcile, not a reason to claw back an entitlement the user already has).
- Settlement (`DONE`) is confirmed the same way as every other transfer in this plan: the transfer-authorization
  webhook (§2.3) approves it by matching the stored intent, and `cmd/reconcile` (extended, same pattern as §5.2's
  processing-withdrawal sweep) polls `wallet_transfer_intents` for `sandbox_purchase_settlement` rows stuck past
  the convergence window, retries submission (idempotent by `externalReference`, matching Invariant #3), and
  alarms if still unresolved.
- This leg is **included**, not excluded, in §6's Invariant #13 conservation check — it's real custody money
  moving between two Asaas accounts CTech controls, exactly the kind of movement that check is built to track.

**Reversal — mirrors §9.2's eligibility rule exactly, reuses the same `AnyDebitSince` check (§9.3), needs no new
purchase-tracking table:** unlike §9.1's direct-PIX purchase (which needed `wallet_sandbox_purchases` because
there is no wallet-ledger record until the sandbox credit lands), a game-funded purchase *is* a wallet transfer
from the moment it happens — the credit ledger entry's own sort key (`credit.SK`, returned by `PurchaseSandbox`
today) is a complete, already-persisted purchase record. Reuse it as the reversal's identifier instead of
minting a new one.

```go
// ReverseSandboxGamePurchase undoes an unused game→sandbox purchase, mirroring
// §9.2's eligibility rule: refundable iff the sandbox wallet has had zero
// outgoing (debit) ledger entries since creditSK, checked with the same
// AnyDebitSince query §9.2 defines. Reverses the ORIGINATING transfer exactly —
// burns the sandbox credits this specific purchase granted, credits `game`
// back the real-money amount it debited — never a general sandbox→game
// conversion route (Invariant #6): this only ever reverses the one named,
// still-untouched transaction, gated the same way §9.2's PIX refund is gated.
// If the caller's game wallet is Asaas-custodied and the forward settlement
// leg (§9.1a) already reached DONE, also reverses the money externally:
// CTech's Asaas master account → the user's subaccount, same
// transfer-intent/authorization/reconcile plumbing, mirrored direction.
func (s *WalletService) ReverseSandboxGamePurchase(ctx context.Context, userID, creditSK, idemKey string) (debit, credit *wallet.LedgerEntry, err error) {
    _, game, sandbox, err := s.requireActivated(ctx, userID)
    if err != nil {
        return nil, nil, err
    }
    ok, err := s.repo.AnyDebitSince(ctx, sandbox.WalletID, creditSK) // §9.3
    if err != nil {
        return nil, nil, err
    }
    if ok {
        return nil, nil, problem.SandboxPurchaseUsed() // §8, reused verbatim
    }
    amount, err := s.repo.AmountAtSK(ctx, sandbox.WalletID, creditSK) // new: read the original credit's amount
    if err != nil {
        return nil, nil, err
    }
    credits := wallet.ToSandboxCredits(amount)
    debit, credit, err = s.ringTransfer(ctx, sandbox, game, credits, amount,
        wallet.EntrySandboxPurchaseReversal, wallet.EntryGameFundReversal, "sandbox_purchase_reversal", idemKey,
    )
    if err != nil {
        return nil, nil, err
    }
    if baas, err := s.baas.GetIfApproved(ctx, userID); err == nil && baas != nil {
        s.reverseGamePurchaseSettlement(ctx, baas, creditSK, amount, idemKey) // symmetric to settleGamePurchaseExternally
    }
    return debit, credit, nil
}
```

- `reverseGamePurchaseSettlement` first checks the original leg's `wallet_transfer_intents` row (looked up by
  `ledger_credit_sk == creditSK`, stored above): if it never reached `DONE` (still `awaiting_authorization` or
  `processing`), mark it `superseded` and stop — no money ever left the subaccount, so there is nothing to
  reverse externally. **Named residual risk, same posture as §6's frozen-counterparty gap:** a race where the
  forward leg reaches `DONE` at Asaas microseconds after being marked `superseded` locally is possible but
  narrow (the convergence window is minutes, per §6) — `cmd/reconcile`'s existing conservation scan (§6) is the
  backstop that catches it, same mechanism that catches every other drift source in this plan, not a new one. If
  the original leg already reached `DONE`, submit the mirror transfer (`master → subaccount`,
  `externalReference = "sbxg-rev#" + idemKey`) through the identical intent/authorization/reconcile path as the
  forward leg.
- New route: `POST /v1.0/wallet/sandbox/game-purchases/:credit_sk/reverse {}` + `Idempotency-Key` — user JWT,
  `requireActivated` gate (same as `PurchaseSandbox` itself; unlike §9.1's direct purchase, this is a ring-fence
  operation and stays behind the gambling gate).
- `wallet.EntrySandboxPurchaseReversal` / `wallet.EntryGameFundReversal` — two new ledger entry type constants,
  same pattern as every other entry type in `domain/wallet`; needed so this reversal is distinguishable in the
  ledger from `ReturnFromGame` (§5, unrelated: that's a user *choosing* to move money out of the ring-fence, this
  is *undoing a specific purchase transaction*) — conflating the two would make the audit trail unreadable and
  would double-count against the gross-inflow limit accounting (Invariant #8) if they were ever confused.
- `repo.AmountAtSK` — new one-`GetItem` repository method (`wallet_ledger_entries` by SK), the read-side
  counterpart to the `AnyDebitSince` `Query` §9.3 already adds; no new table.

**Invariant #6 scope note, stated explicitly per root `CLAUDE.md`'s "if a change appears to require breaking an
invariant, stop and ask":** this reversal *does* credit `game` (real money) from a `sandbox`-side operation,
which is the literal shape Invariant #6 forbids. It does not weaken the invariant in substance: it is not a
conversion route (no arbitrary sandbox balance is ever spendable as game money) — it only ever reverses the one
specific, still-untouched transaction the caller names, gated by the exact same untouched-since-purchase check
§9.2 already uses to carve out its own narrow exception to the same invariant. If this framing is wrong, this is
the point to say so before build — the eligibility gate (`AnyDebitSince`) is the only thing standing between
"transaction reversal" and "conversion route," so it must never be weakened or bypassed.

**Feature-flag scope:** the settlement/reversal legs above only fire once a user's `game` wallet is individually
Asaas-custodied (`s.baas.GetIfApproved` returns non-nil) — for every other user, `PurchaseSandbox` and the new
`ReverseSandboxGamePurchase` behave exactly as `PurchaseSandbox` does today (ledger-only). No new top-level
feature flag needed; this rides the same per-user custody-status branch every other section of this plan already
uses (§4.1, §5.1).

**Testing:**

- Unit: `AmountAtSK` / eligibility gate boundary (zero debits vs. one debit since `creditSK`).
- Integration: full round-trip against a custodied user — purchase (ledger commits, settlement leg reaches
  `DONE` via the fake's transfer-authorization flow) → reverse (eligible: mirror leg reaches `DONE`) → reverse
  attempted after an intervening `DebitSandbox` (rejected, `SandboxPurchaseUsed`, no external call attempted).
- Integration: settlement leg submission failure (ledger already committed, credits already granted, intent row
  stays `awaiting_authorization`, reconcile retries and resolves it) — same shape as §5.3's leg-2-failure test.
- Integration: reversal requested while the forward leg is still `awaiting_authorization` (marked `superseded`,
  no external call) vs. after it reached `DONE` (mirror transfer fires) — both eligibility branches of
  `reverseGamePurchaseSettlement`.
- Regression: assert `PurchaseSandbox`/`ReverseSandboxGamePurchase` for a non-custodied user never call
  `asaas.CreateTransfer` at all — same "assert the gate actually gates" posture as
  `TestSandboxPurchaseNeverDebitsRealWallet`.

### 9.2 Refund policy — resolved

Design spec §9.9 item (b) said a blanket no-refund clause on a paid digital-currency purchase is void under CDC
art. 49 (7-day right of withdrawal on purchases made outside a physical establishment), and flagged that any
reversal mechanism is also an Invariant #6 decision, not just a consumer-law one. Resolved as follows:

**Rule: a purchase is refundable if and only if the sandbox wallet has had zero outgoing (debit) ledger entries
since that purchase's credit landed.** Concretely: for purchase `P` whose credit entry has ledger key
`P.CreditSK`, `P` is refundable iff no entry with `Amount < 0` exists on the sandbox wallet's ledger with a sort
key greater than `P.CreditSK`. This is checkable with **the ledger that already exists** — no new tracking, no
per-purchase earmarking of fungible credits, no FIFO accounting. If the user has spent anything at all since
buying pack `P` (on this or any other pack — credits are fungible, so any spend "touches" every pack bought
before it), `P` is no longer refundable; if they haven't, it is, indefinitely (this plan places no extra time cap
beyond CDC art. 49's own 7-day window not being the ceiling — refunding unused credits later is more generous
than the law requires, not less, and avoids a second edge case (what happens at day 8) for no benefit).

**Refund mechanism — does not touch Invariant #6:**

1. Under the sandbox wallet lock, debit exactly `P.CreditsGranted` credits with a new ledger entry type
   `EntrySandboxRefundReversal` (balance-guarded, `Amount >= P.CreditsGranted` — this is safe because "unused"
   already proved the balance hasn't dropped below what every un-refunded purchase together granted). This is a
   **debit to zero-out the entitlement**, not a conversion — sandbox credits are simply revoked, they do not
   become `game` or `real` money at any point. `sandbox → game`/`sandbox → real` stays exactly as forbidden as
   Invariant #6 already requires.
2. Only after that debit commits, call `pix.Refund(ctx, e2eID, amount, idemKey)` — the same Inter `Refund` method
   `refundExcessPayments`/`rejectMismatch` already call for deposits, `e2eID` sourced from the purchase's payment
   confirmation exactly as those existing call sites already do — to return the PIX payment amount to the payer.
   This is the money side, and it is real money returning to a real bank account — nothing sandbox-shaped ever
   crosses back into `real`/`game`.
3. If step 2 fails after step 1 committed (credits already revoked, refund not yet issued), this is the same
   shape as `rejectMismatch`/`reverseDeposit` elsewhere in this codebase: raise an operational alarm for manual
   reconciliation, never fail silently. Retrying the refund call is idempotent (same `idemKey` derived from
   `purchaseID`, same posture as every other Inter-backed money-out call in this codebase — see `interIdemKey` in
   `wallet.go`), so the reconciliation sweep can simply retry it.
4. Mark the purchase `refunded` only once the Inter refund call itself succeeds.

### 9.3 Design

```
POST /v1.0/wallet/sandbox/purchase              {sku}  + Idempotency-Key   [user JWT; NO KYC gate]
POST /v1.0/wallet/sandbox/purchases/:id/refund  {}     + Idempotency-Key   [user JWT; NO KYC gate]
```

New service method, deliberately NOT on `WalletService`'s existing sandbox methods (`PurchaseSandbox`,
`CreditSandbox`) because those are ring-fence operations gated by `requireActivated`/`EnsureSandboxWallet` tied to
the gambling flow — this is a plain commerce flow that must work for a user who has never touched `game` at all:

```go
// PurchaseSandboxDirect sells a fixed sandbox-credit pack for a fixed PIX price,
// charged via the existing Inter integration to CTech's own pooled account —
// never a wallet-to-wallet transfer, never Asaas (custody is not involved).
// No KYC gate: this is a product sale (CDC), not custody (Res. Conj. 16/2025) —
// see docs/plans/2026-07-30-asaas-baas-implementation-plan.md §9.1 for why that
// distinction is load-bearing, not a convenience.
func (s *WalletService) PurchaseSandboxDirect(ctx context.Context, userID, sku, idemKey string) (*wallet.SandboxPurchase, *pix.Charge, error)

// RefundSandboxPurchase reverses an unused sandbox purchase per §9.2: eligible
// iff no debit has landed on the sandbox wallet since this purchase's credit.
// Revokes exactly the credits this purchase granted, then refunds the PIX
// payment — never a sandbox→game/real conversion (Invariant #6 untouched).
func (s *WalletService) RefundSandboxPurchase(ctx context.Context, userID, purchaseID, idemKey string) (*wallet.SandboxPurchase, error)

// ConfirmSandboxPurchase mirrors ConfirmDeposit's shape (re-query by txid, never
// trust the webhook body for money movement) but for a sale, not custody: no CPF
// gate (no KYC, nothing to match against) and no `real` wallet — confirmed
// payment credits EnsureSandboxWallet(userID) exactly once. See §9.3.
func (s *WalletService) ConfirmSandboxPurchase(ctx context.Context, txid string, sweep bool) error
```

- SKUs are a fixed, server-side table (price centavos → credits granted) — never client-supplied, same
  "never trust the client with a money-shaped number" posture as every other amount in this codebase.
- `pix.CreateCharge(ctx, txid, amount, "")` — the exact same Inter integration `InitiateDeposit` already uses.
  `txid = purchaseID`, generated with a distinguishing prefix (e.g. `sbxp#` vs. a deposit's bare ULID) so
  `pix-gateway` can route the confirmation call correctly (see below). `externalReference` matching works exactly
  as it already does for deposits — no new matching logic.
- Webhook path reuses the plumbing that already exists end-to-end: Inter → `pix-gateway`'s inbound webhook →
  re-query at Inter → `pix-gateway` calls `api`'s internal route. `POST /internal/pix/confirm-deposit` stays
  real-deposit-only (unchanged signature, unchanged behavior — do not overload it); add a sibling internal route
  `POST /internal/pix/confirm-sandbox-purchase` (same M2M caller, `pix-gateway`) → `ConfirmSandboxPurchase`. This
  is the one small `pix-gateway` change this feature needs: dispatch on the `sbxp#` txid prefix to the new route
  instead of the existing one — a one-line addition to logic that already exists, not a new integration.
- **Nota fiscal**: this is a taxable sale of a digital good, unlike every other flow in this codebase (deposits
  and withdrawals move the user's own money; this is CTech revenue). Invoice emission is a new requirement — flag
  to whoever owns billing/tax integration; out of scope for the wallet service itself beyond recording the sale
  with enough detail (SKU, price, timestamp, buyer) to invoice against.
- `wallet_sandbox_purchases` (its own table, §2.4) — deliberately separate from `wallet_pix_deposits`, because a
  deposit is custody (money becomes the user's, held for them) and this is a sale (money becomes CTech's,
  immediately). Naming them the same thing in one table would blur exactly the distinction §9.1 depends on.
  Each row stores `credit_sk` — the sandbox ledger entry key the purchase's credit landed at, so §9.2's
  eligibility check has something to compare against — and `e2e_id` (populated once the webhook/re-query reports
  it), since Inter's `Refund` is keyed by `e2eID`, not by charge/purchase ID, same as every other Inter refund
  call site in this codebase (`refundExcessPayments`, `rejectMismatch`).
- New repository method `AnyDebitSince(ctx, walletID, sinceSK string) (bool, error)` on `WalletRepository`: one
  `Query` on `wallet_ledger_entries` with `ExclusiveStartKey` set just past `sinceSK`, `ScanIndexForward: true`,
  short-circuiting on the first item with `Amount < 0`. This is the entire eligibility check — no new table, no
  per-purchase balance tracking, reuses the append-only ledger that already exists for every other invariant in
  this codebase.

### 9.4 Copy and disclosure — not just an engineering concern, but code must not contradict it

Per CDC art. 54 §4º and the "this is a sale, not a deposit" framing above, the purchase UI/receipt must say
"compra de créditos" / "pacote de fichas de entretenimento — sem valor monetário, não resgatável, não
transferível," never "depósito," "recarga de saldo," or anything implying a stored-value/custodial relationship.
This is a UI/`ui/CLAUDE.md`-scoped concern to raise with the frontend, but the API's own field names
(`SandboxPurchase`, not `SandboxDeposit`) should reinforce it rather than fight it.

### 9.5 Testing

- Unit: SKU table lookup, idempotent purchase-credit (replay never double-credits), `AnyDebitSince` boundary
  cases (no entries at all → eligible; one debit right after credit → ineligible; a debit on an *older* purchase
  should also flip a newer, still-unspent purchase's history correctly given entries are per-wallet not
  per-purchase — write the fungibility case explicitly, it's the one easy to get wrong).
- Integration: webhook → re-query → credit (mirrors the existing deposit test shape exactly, against the `pix`
  fake — same fake, same test scaffolding `InitiateDeposit`/`ConfirmDeposit` already use), payment never
  confirmed (purchase stays `pending`, TTL-swept), duplicate payment against the same charge (excess refunded,
  same pattern as `refundExcessPayments`).
- Refund integration tests: unused purchase → credits revoked + `RefundCharge` called with the right amount;
  purchase with any later `DebitSandbox` → refund rejected (`409`, new problem type, §8); refund replay (same
  idempotency key) → same result, never double-revokes or double-refunds; Asaas refund call failing after the
  credit-revoke debit already committed → alarmed, flagged for reconciliation, retry succeeds idempotently.
- Explicit regression test asserting `PurchaseSandboxDirect` and `RefundSandboxPurchase` **never** call
  `ringTransfer`, never write to a `real` or `game` wallet row, and never appear in the Invariant #13 conservation
  sum — this is the executable form of §9.1's argument, the same spirit as the existing
  `TestSandboxPurchaseNeverDebitsRealWallet` regression test the root `CLAUDE.md` already treats as load-bearing.
  Add a sibling test, not a modification of that one.

---

## 10. Phasing (concretizing design spec §11)

1. **Custody only.** Everything in §2–§4, `AsaasCustodyEnabled` stays `false` outside the Asaas-sandbox test
   environment. `GAMBLING_ENABLED` untouched. Invariant #13 conservation job ships and runs (harmlessly, since
   nothing is on Asaas yet) so its alerting path is proven before it matters.
2. **Settlement + failure modes.** §5–§7. Fee flag still off. This is the phase where the transfer-authorization
   webhook (§2.3), frozen-subaccount handling, closure, and MED clawback all get built and integration-tested
   against Asaas sandbox.
3. **Sandbox-purchase flow.** §9, independent of phases 1–2 — it doesn't touch custody at all, and its refund
   policy is resolved (§9.2), so it ships on its own schedule with no flag and no outstanding legal gate. Built
   against the existing Inter integration, not Asaas (§9.1) — no dependency on any other phase completing.
4. **Fee flag on.** Only after the custody gate *and* a specific legal green light on the withdrawal fee (§0, §5.1)
   — this is a distinct decision from "custody is legally fine," don't bundle them.
5. **Games gate.** Only after signed counsel opinion per §0. Not started by this plan.
6. **Inter decommission for real-wallet custody.** Separate spec, not started here — design spec §12 explicitly
   excludes existing-balance migration mechanics from scope, and this plan doesn't relitigate that. Inter is not
   decommissioned outright: it keeps serving the sandbox-purchase rail (§9) indefinitely — this item only retires
   its use for `real`-wallet deposit/withdrawal once Asaas custody is live.

---

## 11. Spec corrections to carry forward (from the legal audit, not resolved by this plan — just don't repeat them)

When `2026-07-29-asaas-baas-custody-design.md` next gets a substantive edit, fold in the legal audit's five
findings (audit §8): CC art. 814 caput (not 815) governs non-repeatable voluntary payment of a gaming debt; CC
art. 368 compensação is bilateral and does not support 3+-player table netting (§5.4.2/§14.2.6 need reframing
around mandate + game rules, not compensação); CP art. 168 §1º III's ofício/profissão aggravating factor applies
to CTech's custodian role and belongs in counsel's scope; Lei 15.358/2026 art. 21-A's compulsory account-freeze
mechanism is a named risk to the *already-custodied* balances, not just a pre-launch gate, and belongs in §5.5's
frozen-subaccount scenario list; and the Res. Conj. 16/2025 art. 22 contractual-adequacy deadline (31/12/2026) is
a compliance-calendar item, not a code item — someone should confirm the Asaas–CTech contract's signature date
against it.

None of these require an engineering change on their own; they're here so this plan doesn't quietly re-derive or
contradict them later.

Also fold in: §5.1.2's KMS-envelope recommendation is superseded by this plan's §3.3 (local AES-GCM keyed by a
single SSM SecureString) — the two approaches were evaluated against different threat/cost models
(per-user-ciphertext-in-SSM vs. one-master-key-in-SSM), and §3.3 explains why the design spec's rejection of SSM
doesn't apply to the shape this plan actually uses.

---

## 12. Cross-project review

- **api**: this entire plan.
- **cdk**: new SSM SecureString parameter + IAM scoping for the AES master key (§3.3), new DynamoDB tables (§2.4),
  no change to `GAMBLING_ENABLED`-related infra.
- **pix-gateway** (sibling service, not in this repo — confirm location before starting): outbound Lambda extended
  with Asaas actions (§2.2) for custody — the one piece of this plan that touches code outside `ctech-wallet`.
  Separately, the existing inbound Inter-webhook dispatch needs one small addition to route `sbxp#`-prefixed
  txids to the new `POST /internal/pix/confirm-sandbox-purchase` instead of `confirm-deposit` (§9.3) — small,
  unrelated to the Asaas action set above.
- **ctech-account**: internal KYC endpoint extended to return email/phone/address (§3.1).
- **ui**: onboarding-state UI (§4.1), sandbox-purchase product surface with sale-not-deposit copy (§9.4) — not
  designed here, flagged for `ui/CLAUDE.md`-scoped work.
- **Not touched**: ledger/idempotency/locking core, three-wallet topology, `real → game` choke point, existing
  Inter integration, `GAMBLING_ENABLED`'s current behavior.
