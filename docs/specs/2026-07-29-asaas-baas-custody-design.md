# Asaas BaaS + Per-User Subaccounts — Custody Redesign

**Status:** proposed
**Date:** 2026-07-29
**Supersedes:** `docs/specs/2026-07-13-pix-gateway-lambda-design.md` (Inter transport),
`docs/specs/2026-07-13-inter-token-manager-design.md` (dead once Inter is gone)
**Amends:** `docs/specs/2026-07-12-three-wallet-topology-design.md` (custody layer only — the three-wallet ledger model
is unchanged)
**Blocks:** production launch with real third-party money.

---

## 1. Problem — the current model is a custody defect, not a bug

Every real today lands in **one** Banco Inter PJ account owned by A O CARVALHO TECH (CNPJ 62.787.449/0001-07).
The `wallets` table is a private claim ledger against that single pooled balance. Consequences:

| Consequence                                     | Why it is unacceptable                                                                                                                                                              |
|-------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| **Commingling (conta omnibus)**                 | Third-party money is indistinguishable from company money in the same account.                                                                                                      |
| **Bankruptcy / execution pooling**              | The Inter balance is a company asset. A `penhora`, `BacenJud` block, tax execution, or bankruptcy sweeps user funds with it — they are not `bens de terceiros` in any registry.     |
| **Apropriação indébita exposure (CP art. 168)** | The company has *possession* of others' money in its own name. Any use of it — even accidental, even a transfer between own accounts — is the elementary conduct of the crime.      |
| **Unauthorized payment-institution activity**   | Holding and moving third-party funds in own name and settling between users is `arranjo de pagamento` activity (Lei 12.865/2013, Res. BCB 80/2021). CTech is not authorized by BCB. |
| **The terms addendum admits it**                | `docs/legal/wallet-terms-addendum.md` §6 literally says *"atua como intermediário técnico de custódia"* — a claim CTech is not licensed to make.                                    |

The Asaas BaaS + per-user-subaccount model fixes the **custody** layer: money sits in an account **held by the
user**, at an institution authorized by BCB. It does **not** settle the skill-game classification or the mandate layer — see §9.

---

## 2. Current flow (as-is) — what exists today, end to end

### 2.1 Components

```
ui (Next.js)            api (Go, EC2 ASG, IPv6-only ALB)          pix-gateway (2 Lambdas)      Banco Inter
────────────            ────────────────────────────────          ───────────────────────      ───────────
pix-charge-dialog  ──►  POST /v1.0/wallet/deposits          ──►  cmd/outbound (mTLS)      ──► PUT /pix/v2/cob/{txid}
useWalletRealtime  ◄──  GET  /v1.0/ws?token=            ┌──►  cmd/webhook (API GW mTLS) ◄── POST webhook ?hmac=
balance-cards      ──►  GET  /v1.0/wallet/balances      │
                        POST /v1.0/wallet/withdrawals    └──  POST /internal/pix/confirm-deposit
                        cmd/reconcile (ECS/schedule)
```

Money custody today: **one** Inter PJ account. `wallets.balance` (DynamoDB atomic counter) is the only
per-user split — internal bookkeeping with no external counterpart.

### 2.2 Deposit (current)

```
1. UI    POST /v1.0/wallet/deposits {amount} + Idempotency-Key
2. api   InitiateDeposit  (services/wallet.go:201)
         ├─ kycLevel != ""                       → else 403 kyc-not-verified
         ├─ amount <= MaxInboundAmount           → else 422 amount-above-limit
         ├─ ValidateDepositAmount(amount, wallet) → else 422 deposit-out-of-range   [BEFORE opening a charge]
         ├─ ReserveDepositIdem(IDEM#initdep#key) → txid = ULID  [SEC-08: reserve BEFORE the bank call]
         └─ pix.CreateCharge(txid, amount)
              └─ LambdaPixClient → cmd/outbound → PUT /pix/v2/cob/{txid} (expiry 300s, chave = INTER_PIX_KEY)
3. api   PutDeposit{status=pending, TTL=60min}; returns EMV payload + PNG
4. Payer pays the QR
5. Inter POST https://pix.wallet.aoctech.app?hmac=<secret>   (mTLS client cert verified by API GW Trust Store)
6. Lbd   cmd/webhook: constant-time hmac compare → parse txid + pagador.cpfCnpj → POST api /internal/pix/confirm-deposit
7. api   ConfirmDeposit (wallet.go:279)   [Invariant 11 — webhook is a wake-up signal only]
         ├─ UpdateDepositPayer(payerCPF)  ← the ONLY source of payer CPF (Inter re-query drops it)
         ├─ pix.QueryCharge(txid)         ← SOURCE OF TRUTH
         ├─ refundExcessPayments(Payments[1:])            → devolução each, never credited
         ├─ status != CONCLUIDA           → no-op, safe to be re-woken
         ├─ refunded(charge)              → status=refunded, never credit
         ├─ maskedCPFMatches(dep.PayerCPF, kyc.CPF)       → else rejectMismatch + devolução
         ├─ charge.Amount != dep.AmountExpected           → ALARM + rejectMismatch
         ├─ lock.Acquire(walletID)        → else 409 wallet-busy
         ├─ repo.Credit{EntryDeposit, IDEM#deposit#txid}  ← TransactWriteItems
         └─ broadcast deposit_confirmed → Valkey ws:{user_id} → UI invalidates React Query
8. cmd/reconcile sweeps pending deposits older than sweepAgeThreshold → ConfirmDeposit(sweep=true)
   (sweep skips the CPF gate when no webhook ever arrived — SEC-03)
```

### 2.3 Withdrawal (current)

```
1. UI    POST /v1.0/wallet/withdrawals {amount} + Idempotency-Key   [step-up: last_mfa_at < 5min]
2. api   Withdraw (wallet.go:547)
         ├─ kycLevel == verified
         ├─ lock.Acquire(realWalletID)
         ├─ GetWithdrawal(withdrawalID) → replay returns the prior record
         ├─ pixKey = kyc.CPF            ← client NEVER supplies a destination key
         ├─ fee = WithdrawalFee(amount, wallet)   2% / min R$1 / max R$10, absolute floor 100c
         ├─ DebitWithFee → ONE TransactWriteItems: balance debit + 2 ledger entries + IDEM guard + withdrawal row
         │                 (SEC-01: co-written so a crash can never orphan the debit)
         └─ pix.Transfer(pixKey, amount, interIdemKey(withdrawalID))  → POST /banking/v2/pix
              ├─ ErrKeyNotFound  → reverse() immediately, 422 pix-key-not-found
              ├─ other error     → status stays `processing`  [Invariant 12 → cmd/reconcile]
              └─ ok              → status=completed, broadcast withdraw_completed
3. cmd/reconcile: ListProcessingWithdrawals → QueryTransfer(idemKey) → complete or reverse
```

### 2.4 Ring-fence (current, unchanged by this spec)

```
PIX in ──► real ◄── PIX out              real→game  = FundGame     (LIMITED, gross inflow)
            │ ▲                          game→real  = ReturnFromGame (never limited, no fee)
  LIMITED ──┤ │── return (free)           game→sandbox = PurchaseSandbox (one-way sink)
            ▼ │                           game holds/cashout = ctech-poker via M2M scopes
           game ──► sandbox
```

`real`, `game`, `sandbox` are three rows in `wallets`. `real + game` are claims on real money; `sandbox` is
virtual credits (`SandboxCreditsPerCentavo = 10`).

---

## 3. Target model — money is held by the user, at Asaas

```
                    CTech parent Asaas account (CNPJ) — company money ONLY (rake, subscriptions)
                     │  creates subaccounts, holds parent API key, receives ACCOUNT_STATUS_* webhooks
                     │
   ┌─────────────────┼─────────────────┬─────────────────┐
   ▼                 ▼                 ▼                 ▼
 sub(user A)      sub(user B)      sub(user C)  ...   (one Asaas subaccount per user, in the USER'S name+CPF)
 EVP pix key      EVP pix key      EVP pix key         R$13,90 one-off each
 balance = A's    balance = B's    balance = C's       ← THIS is the custody; the money is legally theirs
```

**The new load-bearing invariant (#13):**
> `real.balance + game.balance + Σ(open holds)` for a user **equals** that user's Asaas subaccount balance.
> `sandbox` is excluded (virtual). Any drift is a reconciliation defect and must alarm.

CTech's own account never holds a centavo of user money. That single property is what removes the
commingling, the bankruptcy pooling, and the `apropriação indébita` exposure.

### 3.1 What survives unchanged

The entire ledger core survives. `wallets` / `ledger_entries` / `wallet_audit` / the Valkey lock /
`AcquireOrdered` / the idempotency guards / the three-wallet topology / the responsible-gambling limit engine /
holds and cashout — **none** of it changes. Asaas replaces the *custody and transport* layer only.

`pix-gateway` also survives, for the original reason: **`api.asaas.com` has no AAAA record** (verified
2026-07-29 — IPv4 only, `3.174.26.x`), and `api` runs behind a deliberately IPv6-only ALB. The outbound Lambda
hop stays the cheap IPv4 egress path.

### 3.2 What dies

| Dies                                                                                 | Why                                                                                                                    |
|--------------------------------------------------------------------------------------|------------------------------------------------------------------------------------------------------------------------|
| `internal/inter/*` (mTLS, OAuth token manager, `intertoken.go`, `InterTokenManager`) | Asaas auth is a single static `access_token` header. No mTLS, no OAuth dance, no per-call bearer in `rpc-contract`.    |
| API Gateway mTLS Trust Store + `pix.wallet.aoctech.app` mTLS custom domain           | Asaas webhooks are plain HTTPS authenticated by an `asaas-access-token` header we choose.                              |
| `?hmac=` query-string secret (divergence **B35**)                                    | Replaced by a proper header token (32–255 chars). This is a genuine security improvement, not a lateral move.          |
| `DictAccount` / `DictLookup` dead code (**B30/B36**)                                 | Asaas has `GET /v3/pix/addressKeys/{key}` if same-owner verification is ever wired; the current dead scaffolding goes. |
| `PayerHintCPF` (dead param)                                                          | Never sent to Inter either. Delete.                                                                                    |

---

## 4. Decision: the deposit QR is generated on the **user's subaccount**

The user framed this as the central question. It is not close.

|                                         | QR on CTech's account                                            | QR on the user's subaccount ✅                            |
|-----------------------------------------|------------------------------------------------------------------|----------------------------------------------------------|
| Custody during deposit                  | **CTech holds third-party money**, then transfers                | money is the user's from the instant it settles          |
| The defect this migration exists to fix | **reintroduced** on every single deposit                         | absent                                                   |
| Extra failure mode                      | internal transfer can fail → money in limbo in CTech's account   | none — no transfer step exists                           |
| Static-QR count / rate limits           | all deposits on one account → the ceiling the user worried about | spread across N accounts; the ceiling concern disappears |
| Asaas free-Pix quota                    | one account's quota for all users                                | one quota **per subaccount** (must confirm — §10 Q1)     |
| Payer-facing name on the QR             | "A O CARVALHO TECH"                                              | the user's own name                                      |

The only real argument for the CTech account is the QR showing the company name. That is cosmetic, and it cuts
the other way: a QR in the user's own name is *evidence* the money is theirs, which is exactly the story this
migration tells. Handle it with UI copy, not architecture:

> "Sua conta de pagamentos está no seu nome (CPF ***.***.***-**), aberta para você no Asaas (instituição
> autorizada pelo Banco Central). Ao depositar você está transferindo para a sua própria conta — a CTech nunca
> retém o seu dinheiro."

Paying yourself is already a familiar act (Nubank → Mercado Pago). It reads as safe, not as broken.

**Rejected outright:** deposit into CTech's account + internal transfer. It is the current defect with extra steps.

---

## 5. Flow-by-flow adaptation

### 5.1 Onboarding (the rigid part the user asked for)

```
1. ctech-account KYC reaches `verified`  (manual review, cmd/kyc — level "verified")
2. api  POST /v1.0/wallet/onboarding   (new; or implicit on first deposit intent)
   ├─ read FULL profile from ctech-account internal KYC   ← REQUIRES EXTENDING that endpoint (§5.1.1)
   ├─ POST /v3/accounts (parent API key):
   │     name, email, cpfCnpj, mobilePhone, incomeValue, birthDate,
   │     address, addressNumber, complement, province, postalCode,
   │     webhooks:[{ PAYMENT_*, TRANSFER_*, ACCOUNT_STATUS_* → our endpoint, authToken }]
   ├─ persist asaas_account_id, walletId, accountNumber
   ├─ ENCRYPT + persist apiKey  ← returned ONLY ONCE (§5.1.2)
   ├─ status = `onboarding`   ← NO `real` wallet is created yet
   └─ audit: EventAsaasSubaccountCreated
3. wait >= 15s (Asaas requirement), then GET /myAccount/documents (subaccount key)
   ├─ onboardingUrl present → return it to the UI; user completes KYC AT ASAAS
   │                          ("never send via API a document that has onboardingUrl")
   └─ absent                → POST /myAccount/documents/{id} with our stored KYC docs
4. Asaas compliance reviews (selfie + ID)
5. Webhook ACCOUNT_STATUS_APPROVED (general=APPROVED, and commercialInfo/documentation/bankAccountInfo=APPROVED)
   ├─ POST /v3/pix/addressKeys {type:"EVP"}   ← 1 key per account; BCB allows 1 creation/minute → serialize+retry
   ├─ persist pix_address_key (cached; never re-derived per deposit)
   ├─ EnsureRealWallet(userID)   ← the `real` wallet is BORN HERE, not on first balance read
   └─ audit: EventWalletActivated
```

**Change of shape:** today `EnsureRealWallet` is called implicitly by `GetBalances` and `InitiateDeposit`. That
must stop. A `real` wallet may not exist before a custody account exists to back it — otherwise Invariant #13 is
false from birth. `GetBalances` returns an `onboarding` state instead of creating a wallet.

Gambling activation is unaffected and stacks on top: verified KYC + gambling addendum + limits + **approved
subaccount**.

#### 5.1.1 KYC gap — exactly one field is missing

Mapping `ctech-account`'s KYC record (`internal/domain/kyc/model.go`, `address.go`) to `POST /v3/accounts`:

| Asaas field                                | ctech-account source                         | Status                                                |
|--------------------------------------------|----------------------------------------------|-------------------------------------------------------|
| `name`                                     | `LegalName`                                  | ✅                                                     |
| `email`                                    | user email                                   | ✅                                                     |
| `cpfCnpj`                                  | `CPF`                                        | ✅                                                     |
| `mobilePhone`                              | `PhoneNumber` (E.164)                        | ✅ needs format strip                                  |
| `birthDate`                                | `BirthDate`                                  | ✅                                                     |
| `address` / `addressNumber` / `complement` | `Address.Street` / `.Number` / `.Complement` | ✅                                                     |
| `province`                                 | `Address.District`                           | ✅ (bairro)                                            |
| `postalCode`                               | `Address.ZipCode`                            | ✅ (8 digits, validated)                               |
| `incomeValue`                              | —                                            | ❌ not in KYC — collected by the **wallet**, see below |

**`incomeValue` does not belong in `ctech-account`.** It is an Asaas cadastral field, not an identity attribute,
and `ctech-account`'s KYC is the identity service. Collect it in the **wallet's own real-wallet activation form**
(the same request that triggers subaccount creation) and pass it straight through.

Better still: **do not persist it.** It is needed exactly once, at `POST /v3/accounts`, and it is sensitive
(renda). Send it and drop it; if an audit trail is wanted, append a coarse bucket to `wallet_audit`, never the
value. Nothing downstream reads it.

So the `ctech-account` change reduces to one thing: extend the internal KYC read — `kycclient.KYC` currently
returns only `Level, CPF, LegalName, BirthDate` — to also return **email, phone and address**. `Address.City` /
`.State` come along as the fallback Asaas requires when a CEP does not resolve. No new KYC field, no new form
there, no migration.

#### 5.1.2 Subaccount API-key storage — do NOT use Secrets Manager

One key per user, returned once, and the parent can only rotate it inside a **2-hour window manually enabled in
the Asaas web UI** (BaaS clients excepted — confirm, §10 Q4). Losing a key is therefore expensive.

- Secrets Manager: ~US$0.40/secret/month → 10k users ≈ US$4k/month. **No.**
- SSM SecureString: free at standard tier but capped at 10k parameters per region. A hard user ceiling. **No.**
- ✅ **KMS envelope encryption, ciphertext in DynamoDB** (`asaas_account` item on the user's partition): one CMK,
  `kms:Decrypt` on the `api` role only, ciphertext never logged, no per-user resource. Scales, cheap, auditable
  via CloudTrail.

### 5.2 Deposit (target)

```
1. UI    POST /v1.0/wallet/deposits {amount} + Idempotency-Key           [unchanged contract]
2. api   InitiateDeposit
         ├─ same gates: kyc, MaxInboundAmount, ValidateDepositAmount  ← keep the pre-charge check
         ├─ NEW gate: subaccount status == approved                   → else 409 wallet-onboarding
         ├─ ReserveDepositIdem → txid = ULID                          [unchanged]
         └─ POST /v3/pix/qrCodes/static  (SUBACCOUNT api key)
                { addressKey: <cached EVP>, value, description: "Saldo na CTech Wallet",
                  format: "ALL", expirationSeconds: 900,
                  allowsMultiplePayments: false, externalReference: txid }
3. api   PutDeposit{ txid, asaas_qr_code_id: resp.id, status=pending, TTL }
         └─ ⚠ PERSIST THE MAPPING qr_code_id → txid  (see the correction below)
4. Payer pays
5. Asaas POST our webhook, header asaas-access-token, event PAYMENT_RECEIVED
6. api   /internal/asaas/webhook → resolve account.id → user; resolve payment.pixQrCodeId → txid
7. api   ConfirmDeposit(txid)                    [Invariant 11 preserved, verbatim in spirit]
         ├─ GET /v3/payments/{id} (SUBACCOUNT key) ← SOURCE OF TRUTH, re-query, never the webhook body
         ├─ status must be RECEIVED (not CONFIRMED — funds not yet available)
         ├─ payer CPF: GET /v3/customers/{payment.customer} → compare against kyc.CPF   (§5.2.2)
         ├─ value != dep.AmountExpected → ALARM + refund
         ├─ lock.Acquire → repo.Credit{ amount = payment.netValue }   (§5.2.1 — NOT `value`)
         └─ broadcast deposit_confirmed                              [unchanged]
```

#### 5.2.1 Credit `netValue`, never `value`

The R$1,99 Pix tariff is debited from the account that received the payment — the **user's own subaccount**. If
the ledger credits `value`, then `wallets.balance` exceeds the real custody balance by 199 centavos **per
deposit**, permanently, and Invariant #13 is false. Credit `netValue`.

This is also self-correcting for the free-transfer quota: in the user's own test, `value: 10` and
`netValue: 10` — the tariff was free-quota-absorbed and `netValue` reported the truth with zero prediction
logic on our side. `netValue` is the only field that is always right.

#### 5.2.2 Matching: `externalReference` propagates — verified

The static QR's `externalReference` **is** inherited by the auto-created `cobrança`. Verified against a live
capture (2026-07-29, QR `CTECH00000000678362937ASA`):

```json
"pixQrCodeId": "CTECH00000000678362937ASA",
"externalReference": "01KYR67KN816JBEYPQT3DBZ7WM", ← the ULID txid, round-tripped
"status": "RECEIVED"
```

So the primary match is **`payment.externalReference == txid`** — a direct lookup on the existing `PixDeposit`
key, no extra index needed.

Persist `asaas_qr_code_id` on the deposit row anyway, as a **secondary** match. It costs one attribute and
covers the two cases `externalReference` cannot:

1. A QR created before `externalReference` was set (an earlier capture from the same account shows
   `externalReference: null` with a populated `pixQrCodeId` — that QR was created without the field).
2. A **third-party Pix straight into the user's Pix key**, with no QR at all — a real possibility now that the
   key is a live EVP on a real account. Neither field resolves to a known deposit. That is not an error: log it,
   credit nothing automatically, and put it through the CPF gate as an unsolicited inbound payment.

**Both `PAYMENT_CREATED` and `PAYMENT_RECEIVED` fire for a paid static QR, and the capture shows
`PAYMENT_CREATED` already carrying `status: RECEIVED`.** Do not branch on the event name: resolve the txid, then
re-query and act on the re-queried status. `ConfirmDeposit` is idempotent per txid, so the duplicate event is a
no-op by construction — but a dynamic charge's `PAYMENT_CREATED` arrives `PENDING`, so the re-queried status
(`RECEIVED`, never `CONFIRMED`) must remain the only gate.

### 5.3 Withdrawal (target)

```
1. UI    POST /v1.0/wallet/withdrawals {amount}      [step-up MFA 5min — unchanged]
2. api   Withdraw
         ├─ lock, replay check, kyc.CPF as destination      [all unchanged]
         ├─ settleCustody(userID)   ← NEW: make custody match the ledger before money leaves (§5.4)
         ├─ fee = WithdrawalFee(amount, wallet)             2% / min R$2,50 / max R$10  [§6.4]
         ├─ DebitWithFee(amount, fee)                       [same single TransactWriteItems]
         └─ POST /v3/transfers (SUBACCOUNT key)
                { value, pixAddressKey: kyc.CPF, pixAddressKeyType: "CPF",
                  externalReference: withdrawalID, operationType: "PIX" }
              ├─ Asaas calls our TRANSFER-VALIDATION webhook (§5.3.2) → we APPROVE/REFUSE
              ├─ status PENDING/BANK_PROCESSING → stays `processing`   [Invariant 12]
              ├─ status DONE  → completed, then sweep the 2% fee to the parent account (§6.4.2)
              └─ FAILED/CANCELLED → reverse (amount + fee; no sweep happened yet)
3. cmd/reconcile: GET /v3/transfers?externalReference=<withdrawalID> → complete or reverse
```

#### 5.3.1 ⚠ Regression to close: `/v3/transfers` has **no idempotency key**

Inter took `x-id-idempotente`, and `interIdemKey(withdrawalID)` (a UUID v5, marked *DO NOT EVER CHANGE*) made
the payout idempotent **at the bank**. Asaas offers no equivalent header. A retry after a timeout can therefore
send the same Pix twice — a double payout, which is the worst failure mode this service has.

Mitigation (mandatory, not optional):

1. `externalReference: withdrawalID` on every transfer.
2. **Before** any `POST /v3/transfers`, `GET /v3/transfers?externalReference=<withdrawalID>`; if a transfer
   exists in any state, never re-send — adopt it.
3. Persist `asaas_transfer_id` on the withdrawal row the moment the response is parsed.
4. Treat *any* ambiguous outcome (timeout, 5xx, connection reset) as `processing`, never as a retryable failure.
   The reconcile job resolves it by `externalReference`.
5. The transfer-validation webhook (below) is the final backstop: it can refuse a duplicate it does not
   recognise.

This must land with an integration test named for what it prevents (mirroring
`TestSandboxPurchaseNeverDebitsRealWallet`): **`TestWithdrawNeverSendsTwoTransfersForOneWithdrawal`**.

#### 5.3.2 Enable the transfer-validation webhook — it is worth more than IP whitelisting

Asaas can call our endpoint before executing any transfer and refuse unless we reply
`{"status":"APPROVED"}` (3 failed attempts → cancelled). This gives us: *a leaked subaccount API key cannot move
money*, because authentication alone no longer authorizes a transfer. We validate against our own ledger — the
withdrawal row exists, is `processing`, amount and destination match, not already sent.

Asaas's IP-whitelist feature would need a fixed egress IP (NAT Gateway / EIP → real monthly cost, and the whole
point of the non-VPC Lambda was avoiding IPv4 charges). The validation webhook is free and defends better.
Choose the webhook. Their published official IPs still get an allow-list on our webhook endpoint's WAF.

### 5.4 Games and holds — custody drift and when to converge it

This is the part with no analogue in the Inter model, and the design turns on one confirmed fact:

> **A BaaS subaccount holder has no Asaas panel, no login, and no API key.** Every movement is executed by the
> parent account. (Confirmed with Asaas, 2026-07-29.)

That fact changes the problem completely. When A loses R$100 to B, the money is still physically in **A's**
subaccount while the ledger says it is B's — but A cannot touch it. The exits are:

| Exit                          | Gate                                                           |
|-------------------------------|----------------------------------------------------------------|
| Asaas panel / direct Pix by A | **does not exist** — no access                                 |
| `POST /wallet/withdrawals`    | our API: open hold + `ConditionExpression: balance >= :amount` |
| Anything else                 | there is nothing else                                          |

So internal drift is **not** a solvency risk and does not need per-hand chasing. It is a *legal-attribution* risk
only, and its size is a function of the drift **window**, not of table activity.

#### 5.4.1 Rejected approaches

- ❌ **Transfer per hand/round.** Chips move continuously and a hand in progress has money in a pot that belongs
  to nobody yet. Settling mid-hand settles a fiction. Also: hundreds of API calls for a state that is about to
  change again.
- ❌ **Pot/omnibus account per table.** Buy-ins into a CTech-owned "mesa" account reintroduces commingling for
  the single most scrutinized category of money in the system. Rejected on the same grounds as the whole
  migration.
- ❌ **Never converge.** Attribution exposure grows without bound and Invariant #13 becomes decorative.

#### 5.4.2 The model: hold-anchored custody, converged at safe points

**The hold is the custody anchor.** At buy-in, `HoldGame` debits `game` and the money stays where it is,
immobilized. Nothing physical happens. This is already the current behaviour — no change.

**Settlement is a recorded obligation before it is a transfer.** When the poker engine settles (`CashoutGame`,
table close), the ledger entries are written first, in the same `TransactWriteItems` as always. The internal
transfer that follows is the *execution* of an obligation that is already durable. A failed transfer therefore
never loses the record — it retries. That ordering is what answers *"como garantir que o dinheiro de A realmente
vai para B"*: the guarantee is not the transfer, it is the ledger entry that precedes it and the sweep that
insists on it.

**Converge at three points, in this priority:**

1. **Table close (`settlement batch`).** All stacks are final, no hand in progress, amounts are settled. Compute
   the **net delta per player** for the session and emit a *netted* set of internal transfers: net losers → net
   winners, greedy-matched, at most `N-1` legs for `N` players instead of `N²`. The platform's **fixed table entry fee** — not a rake; real-money tables take no share of the pot
   (§9.4, Invariant #15) — is one more leg in the same batch, and that leg is legitimate company revenue arriving
   in a company account.
2. **Mandatorily before any withdrawal** (`settleCustody`, §5.3). This is the only moment custody must actually
   be true, because money is about to physically leave the platform. If the subaccount is short, pull the deficit
   from the surplus accounts the ledger identifies, then transfer out.
3. **Periodic sweep** (`cmd/reconcile`), for long-running tables and anything the first two missed: compare
   `real + game + Σholds` against `GET /v3/finance/balance` per subaccount, converge drift, alarm on anything it
   cannot explain. A mid-session sweep may only settle **between hands** — the poker engine must expose a
   "no hand in progress" signal, and a table without one is simply skipped until close.

`POST /v3/transfers {value, walletId}` between accounts under the same parent is **free and effectively
instant**, so the cost driver is call count and failure surface, not fees — which is exactly why netting at close
beats chasing rounds.

#### 5.4.3 The settlement batch must be idempotent

Same hazard as §5.3.1 (`/v3/transfers` has no idempotency key), and worse because a batch has many legs. Shape:

- A `settlement` row per batch: `batch_id` (ULID), table ref, the computed legs, status per leg.
- Each leg carries `externalReference = <batch_id>#<leg_n>`.
- Before sending any leg, `GET /v3/transfers?externalReference=<batch_id>#<leg_n>` — adopt an existing transfer,
  never re-send.
- A leg that fails or is ambiguous stays `pending`; the sweep retries it. Partial batches are normal and safe,
  because the ledger already reflects the full settlement.

#### 5.4.4 The residual risk, stated honestly

The one real exposure left: A's subaccount is **frozen by a court order, death, or Asaas compliance** while
holding money the ledger attributes to B. Nothing internal prevents that — it is external to the system.
Mitigations, all of which this design already requires:

- **Bound the window.** Converge at table close, not at "eventually". Minutes, not days.
- **Subscribe to Asaas's balance-block webhook events** and mark the wallet `frozen` (§5.5).
- **Fail closed at the system level.** Monitor `Σ subaccount balances == Σ (real + game + holds)` as a
  system-wide conservation check. If it breaks, block **all** withdrawals until reconciled — never let a
  shortfall be discovered by the unlucky user who withdrew last.
- Accept and document that a frozen counterparty account can delay a withdrawal, and size an operational
  reserve for it.

Pulling money out of A's subaccount to pay B is only lawful because of the **mandate** (§9.2). No mandate, no
settlement batch. That clause is load-bearing infrastructure, not paperwork.

### 5.5 New failure mode: a frozen or blocked subaccount

With an omnibus account, a judicial block was catastrophic but singular. Now a block can hit **one user's**
subaccount — and Asaas emits balance-block webhook events for exactly this. The ledger must model it:
a `frozen` state on the wallet, withdrawals and settlements refused with a distinct problem type
(`account-blocked`), and an operator surface. Silently failing a withdrawal because the destination account is
frozen is precisely the "money in limbo" Invariant #12 forbids.

### 5.6 Revocation of the mandate / account closure — **does not exist and must be built**

Counsel's position (2026-07-29): *the mandate is only valid if it is revocable and revocation has real effect.*
That makes this flow a **launch blocker**, not a nice-to-have. Verified against `api/internal/api/v1/router.go`:
there is **no closure or revocation route today** — the closest things are `gambling/self-exclude` (blocks play,
keeps the account) and `gambling/limits`. Nothing returns the balance and nothing tears down the account.

New route: `POST /v1.0/wallet/closure` — user JWT, `RequireRecentMFA` (it moves the entire balance out), and an
`Idempotency-Key`. Steps, in order:

1. **Refuse if not settleable.** Open holds, a hand in progress, or a `processing` withdrawal → `409`. Revocation
   cannot outrun §5.4's convergence; it *forces* it (`settleCustody`) and then re-checks.
2. **Converge custody** — run the settlement of §5.4.2 so the subaccount balance equals `real + game`.
3. **Freeze the wallet** — a terminal `closing` state. No deposits, no funding, no play, no new holds. Set
   *before* the payout so nothing races the drain.
4. **Pay out the whole balance** to the user's own verified-CPF Pix key. Reuses the withdrawal transport
   (§5.3), including the no-idempotency-key mitigation of §5.3.1. **The closure payout carries no fee and is
   funded up to the transfer cost if the balance cannot cover it** (§6.4.1): charging a consumer to leave reads as
   a penalty for termination, and waiving it deletes the dust-balance trap, the waiver branch and the residue
   write-off in one move.
5. **Sweep any residue** and close the subaccount at Asaas via API. Do not close before the payout is `DONE`.
6. **Disable the wallet** for that user: `closed`, `mandate_revoked_at`, and a `wallet_audit` row
   (`mandate_revoked`) carrying the accepted mandate version being revoked.

**Do not make it a single request.** Each step is externally-visible and independently retryable; model it as a
state machine on the wallet row (`closing → paid_out → subaccount_closed → closed`) driven by the same
reconciliation job as §5.3, so a failure between the payout and the Asaas closure resumes rather than stranding
the user half-closed. A closure stuck with money already sent but the account still open is Invariant #12
territory.

**Reopening** is a new subaccount and a new R$13,90 (§6.5) — and a fresh mandate acceptance. State that in the
UI before the user confirms.

Extinction of the mandate is not only by revocation: **CC art. 682 IV — the mandate ends on the mandante's
death.** At that point CTech has *no* authority to move the balance and succession law applies (§9.8). Support
needs a written rule for this, and the `frozen` state of §5.5 is the mechanism. Counsel's draft v2.2 clause 7
already carries this ("extingue-se automaticamente pela morte"), so it is contracted before it is built.

### 5.7 MED — a credited deposit can be clawed back, and the ledger cannot go negative

Counsel's draft v2.2 §5 contracts for the *Mecanismo Especial de Devolução*: a Pix received can be reversed after
the fact by the payer's bank, on fraud grounds, up to 80 days. Under the omnibus model this was CTech's problem
and CTech's balance. Now the debit lands on the **user's subaccount**, and the wallet has no reversal path at all.

The collision is direct and there is no clever way around it:

- **Invariant #2** — `ledger_entries` is append-only, so a MED reversal is a new compensating debit, never an edit.
  That part is fine.
- **Invariant #1** — balance may never go negative. But by the time MED fires the user has very likely spent the
  money: funded `game`, bought sandbox, or withdrawn it. The compensating debit **cannot succeed**, and Asaas will
  debit the subaccount regardless.

That leaves the subaccount short against a non-negative ledger — the exact inverse of the drift in §5.4, and the
one case where custody goes *below* the ledger. Required handling:

1. **Detect it.** Subscribe to the Asaas event for it (§10 — add as Q12: which webhook event, and what the
   notification window is). Do not discover MED from a balance mismatch.
2. **Debit what is there**, conditionally, exactly as today; the shortfall becomes an explicit
   `med_receivable` row against the user — a *debt*, not a negative balance. Invariant #1 stays literally true:
   `wallets.balance` never goes below zero.
3. **Block withdrawals and funding while a `med_receivable` is open**, and settle it from the next inflow. This is
   the only place in the system where the wallet holds a claim against the user, so it must be a distinct concept
   with its own problem type, not a negative number smuggled into a balance.
4. **A `med_receivable` must never be netted into the conservation check** of §5.4.4 without being named, or the
   check silently stops meaning anything.

Cheapest mitigation, and it is worth more than the machinery above: the CPF-match rule already rejects
third-party deposits (§2 of the addendum), and MED overwhelmingly targets payments from a defrauded third party.
Same-CPF-only deposits are a strong structural filter. It is not a complete one — a user whose own account was
taken over is still a MED case — so the receivable path has to exist.

**⚠ Related hole in the same section: refunding a rejected deposit costs money that nobody has agreed to pay.**
When a third-party deposit is auto-refunded, Asaas has *already* debited the receiving tariff from the user's
subaccount. Refunding the full amount drives that subaccount negative by the tariff. Counsel's draft handles it by
deducting costs from the refund ("deduzidos eventuais custos operacionais") — but the payer is a stranger who
never contracted with CTech, and returning less than was sent to someone with no contract is a fight not worth
having over R$1,99. **Refund in full and cover the tariff with a parent → subaccount transfer**, booked as an
operating cost. That is a transfer leg that does not exist in this spec yet, and it belongs in `refundExcessPayments`
(`api/internal/services/wallet.go:416`).

---

## 6. The fee question — answered

> *"A principal questão é a taxa de processamento, deveria cobrar a taxa de processamento do usuário diretamente logo?"*

**It is already charged to the user, mechanically, and that is the right answer — but do not add a CTech fee on
top of it. And because the free quota is per subaccount (confirmed 2026-07-29), for almost every real user the
tariff is R$0 and there is nothing to charge at all.**

> **Confirmed:** each subaccount has its own quota — roughly 100 free received Pix and ~30 free transfers per
> month, *per user*. A normal user will never exhaust it. That collapses the fee from "a permanent per-transaction
> cost" to "an edge case for heavy users", and it is the single most consequential answer in §10.

### 6.1 Why it is already the user's

Once the QR lives on the user's subaccount, Asaas debits R$1,99 from **that** account. `netValue < value`.
Crediting `netValue` (§5.2.1) means the user bore the cost. There is no decision to make about *who pays* — only
about whether CTech **reimburses** it. Recommendation: no.

### 6.2 On deposits, do not invoice the tariff as a CTech charge

Deposits carry **no CTech fee** (decided; the withdrawal fee is where the platform earns — §6.4). So for the
deposit path the framing is simply:

> The tariff is **Asaas's**, debited by Asaas from the user's own account, disclosed by us.

Charging separately *for receiving a Pix* would be charging for a payment service CTech is not authorized to
provide. Disclose, never invoice. (This is a framing rule about **what the charge is for**, not a prohibition on
charging — see §6.4, where a platform service fee is a different thing entirely.)

### 6.3 Minimum deposit — no change needed, but the tail must not be a surprise

With a per-subaccount quota, the ordinary deposit is free and `min_deposit` can stay where it is. The tariff only
appears past ~100 received Pix in a calendar month for that one user, and when it does it is automatic and
correct: `netValue` reports it, we credit `netValue`, the user bore it (§5.2.1) — zero prediction logic.

What must not happen is the user discovering it silently. Two requirements:

1. **Track the per-subaccount monthly received-Pix count** (same shape as the existing
   `GameDepositCounters` calendar-window counters on the user row — reuse, do not invent). Past the free quota,
   the deposit dialog shows the arithmetic *before* generating the QR:

   > Você envia **R$ 50,00** · Tarifa Pix (Asaas) **R$ 1,99** · Crédito na carteira **R$ 48,01**

2. **Never deduct silently.** Below the quota show nothing; past it show the line above. Silent deduction from a
   deposit is a `Procon` / CDC art. 6º III complaint waiting to happen.

For reference, if the quota ever does not apply: R$1,99 is 19,9% of a R$10 deposit and 1,99% of R$100. If Asaas
ever changes the quota model, raising the default `min_deposit` (admin-only, per-wallet, already implemented) is
the one-line lever — no code change.

### 6.4 Withdrawal fee — **percentage, 2% / min R$2,50 / max R$10**

**Decided (reaffirmed 2026-07-29).** The withdrawal fee stays *ad valorem*: `clamp(amount*200/10000, 250, 1000)`.
It is what funds the wallet's operating cost — R$13,90 per subaccount, the Asaas Pix tariff past quota,
infrastructure, reconciliation and support. Deposits stay free (§6.2), so it is the only wallet-side revenue, and a
flat fee would collapse it on exactly the large withdrawals that carry the cost (R$10 → R$2,50 on a R$10.000
withdrawal).

**This is the one place the platform keeps a percentage fee, and the contract must say so.** Everything else is
fixed per operation, including real-money poker: those tables are **rake-free** (`hand.ConfigureRake` sets
`rakeBPS = 0` for real money) with a fixed entry fee per tier (R$1/2/4/8, `ctech-poker`
`api/internal/api/v1/stakes.go`). So mandate clause 7 item V may **not** be drafted as "todas de natureza fixa por
operação" — see §9.2.2 item 1.

**Code impact: one constant.** The `clamp(amount*bps/10000, min, max)` shape and the per-wallet
`fee_bps`/`fee_min`/`fee_max` overrides are unchanged.

| Constant           | Was  | Becomes          |
|--------------------|------|------------------|
| `fee_bps` default  | 200  | 200 (unchanged)  |
| `fee_min` default  | 100  | **250**          |
| `fee_max` default  | 1000 | 1000 (unchanged) |
| `FEE_ABSOLUTE_MIN` | 100  | **250**          |

The floor rises because the fee is now **gross** and must absorb the R$1,99 Asaas tariff: at the old 100c floor a
R$50 withdrawal charged R$1,00 against a possible R$1,99 cost, losing money on the most common withdrawal. 2%
reaches R$2,50 at R$125, so the floor governs below that and the percentage above. Mirror in
`ui/src/lib/utils/fee.ts` (divergence **B18**) and move the root `CLAUDE.md` "absolute floor of 100 centavos" line
to 250.

**Call it what it is.** In the addendum and the UI this is a **tarifa de serviço da carteira** — custody
operation, reconciliation, support — which explicitly covers the partner institution's Pix tariff. It is not a
"tarifa Pix". Charging for your own software service is unquestionably lawful; charging *for the transfer* invites
the unauthorized-payment-service argument for no benefit. Same money, one word.

#### 6.4.1 Two consequences of the R$2,50 floor

The percentage self-scales, so the revenue and proportionality problems of a flat fee do not arise. The **floor**
still creates two:

1. **⚠ The floor can exceed the balance, and that traps money.** A user holding R$0,80 can never withdraw:
   `DebitWithFee` needs `amount + 250` and the conditional debit fails forever. The R$1,99 Asaas tariff cannot be
   paid either, so the account cannot even be emptied — which **breaks §5.6 step 4** and the standing rule that
   nothing traps a user's own money. Required rule: **the closure payout is fee-free, always.** CTech absorbs the
   R$1,99 tariff — it already absorbs R$13,90 to open the account — and when the balance is smaller than the
   tariff, CTech funds the difference with a parent → subaccount transfer so the payout can carry **100%** of the
   user's money, booked as an operating cost with a `wallet_audit` entry. Same answer as the third-party refund in
   §5.7, for the same reason, and strictly simpler than the alternative: no `balance < fee` branch, no waiver
   flag, and nothing swept out of a closing user's account. Writing that residue off *to CTech* would be
   appropriation of a customer's money wearing an accounting word — see §9.9 (d).
2. **Proportionality at the bottom of the range.** R$2,50 on a R$3,00 withdrawal is 83% — the percentage is
   irrelevant there because the floor governs, and a fee consuming most of the principal is the shape CDC art. 51 IV
   (*vantagem manifestamente excessiva*) is written about. Clean fix: a **minimum withdrawal amount**
   (`min_withdrawal`, per-wallet like `min_deposit`) set so the effective fee never exceeds ~10% — **R$25**. It also
   removes case 1 for every ordinary withdrawal. Cheaper than arguing that R$2,50 on R$3,00 is proportionate.

   **⚠ `min_withdrawal` must not apply to a full-balance withdrawal.** Otherwise it replaces the dust trap with a
   larger one: a user holding R$20 could not withdraw at all and would have to *close the account* to reach their
   own money — a restriction on access to one's own funds (CDC art. 51 IV), and the same defect one order of
   magnitude up. Rule: the minimum is waived whenever the requested amount equals the entire available balance.
   With that carve-out plus the fee-free closure, no balance is ever unreachable — see §9.9 (e).

#### 6.4.2 ⚠ The fee no longer arrives by itself — it needs its own transfer leg

Under Inter this was free: the fee was debited from `wallets.balance` and the money was already sitting in CTech's
own omnibus account. **That is no longer true.** The fee is debited from the ledger, but the centavos are
physically in the **user's** subaccount. Without an explicit move:

```
ledger:   balance -= amount + fee          (DebitWithFee, unchanged)
custody:  subaccount -= amount + asaasTariff
drift:    fee − asaasTariff  ← permanent, per withdrawal, and it is CTech's own money left in a user's account
```

So a withdrawal is now **two legs plus the tariff**:

| Leg          | Call                                           | Amount                    | Cost                                                                          |
|--------------|------------------------------------------------|---------------------------|-------------------------------------------------------------------------------|
| 1. payout    | `POST /v3/transfers` `pixAddressKey = kyc.CPF` | `amount`                  | `T` = R$0 or R$1,99 (Asaas debits it from the same subaccount, automatically) |
| 2. fee sweep | `POST /v3/transfers` `walletId = <parent>`     | **`fee − T`** — see below | **R$0** (internal)                                                            |

Leg 2 is the same mechanism as the poker rake (§5.4.2), and for the same reason: it is the one path by which
company revenue legitimately reaches a company account.

**The swept amount is `fee − T`, not `fee`.** The custody arithmetic:

```
ledger:   C -= amount + fee
custody:  S -= amount + T          (T is debited from the SAME subaccount by Asaas)
drift  =  fee − T                  ← CTech's net margin, and exactly what leg 2 moves
```

Sweeping `fee` would overdraw by `T`. Recognizing this is what fixes the sweep amount, and `T` is only known from
leg 1's response (`transfer.transferFee`).

**Ordering: sweep AFTER the payout reaches `DONE`.** The tempting alternative — sweep the fee first, then pay
out — is worse on three counts:

1. **It can starve the payout of the tariff.** A user withdrawing their entire balance holds exactly
   `amount + fee`. Sweep `fee` first and the subaccount holds `amount`, but the payout needs `amount + T` — the
   transfer fails for insufficient funds on precisely the operation users perform most.
2. **`T` is unknown beforehand.** Sweeping first requires predicting it: reserve 199, sweep `fee − 199` before,
   then sweep the remaining 199 after if the tariff turned out free. Two legs on the happy path instead of one.
3. **A failed payout would need a compensating transfer.** Parent → subaccount, to undo a sweep for a withdrawal
   that never happened. Sweeping after has no unhappy-path leg at all: no payout, no fee.

**The fee is earned at `DebitWithFee`, not at the sweep.** The ledger entry is the revenue recognition; the sweep
is cash management. That is why "secure the fee before the money leaves" solves a problem that does not exist — if
the payout fails the withdrawal is reversed and there is no fee to collect, and a fee collected for a withdrawal
that did not happen would have to be refunded anyway.

Requirements on leg 2:

- **Idempotent** — `externalReference = <withdrawalID>#fee`, with the §5.3.1 pre-check before sending.
- **Gated on leg 1 being `DONE`**, so the existing `reverse()` path (which credits `amount + fee` back) never has
  to chase money already moved out.
- **Non-fatal.** A failed sweep is drift of a few reais of *CTech's own* money in a user's account — no user is
  harmed and no invariant about user funds is broken. Leave it to the reconcile sweep; never fail a completed
  withdrawal over it. The only way this money becomes unrecoverable is an external freeze on that subaccount,
  which would block either ordering equally.

#### 6.4.3 Predicting the tariff

The Asaas tariff is only known from the transfer response, but the fee must be debited before the transfer
(Invariant #1 ordering). That is not a user-visible problem: the charge is `clamp(amount*200/10000, 250, 1000)` on
the requested amount and does not depend on the tariff at all, so it is fully determined before the transfer is
sent. `T` only moves CTech's own margin. So:

- Debit `amount + WithdrawalFee(amount, wallet)` exactly as today. **No prediction, no rebate, no
  `withdraw_fee_adjust` entry.** The user's charge does not depend on the quota.
- Record `transfer.transferFee` (`T`) on the withdrawal row. It serves two purposes: the cost side of the margin
  for accounting, and the input to the sweep amount `fee − T` (§6.4.2). Keep the per-subaccount monthly counter
  only as an operational metric.

This is strictly simpler than the passthrough model it replaces — the free quota becomes pure margin instead of a
number the ledger has to guess at.

### 6.5 The R$13,90 subaccount fee is CTech's, not the user's

It is charged to the **parent** account. Two consequences:

- **Never create a subaccount at signup.** Create it when the user actually reaches verified KYC and intends to
  transact. Otherwise every curious signup costs R$13,90.
- Absorb it as customer-acquisition cost. Billing R$13,90 to open an account is a conversion killer, and
  charging a user to open an account in their own name is a bad look for the exact trust story this migration
  buys.

---

## 7. Invariant impact — all 12, plus 3 new

| #      | Invariant                                               | Under Asaas                                                                                                                                                                                                                                                             |
|--------|---------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| 1      | Balance never negative                                  | **unchanged** — same conditional `TransactWriteItems`                                                                                                                                                                                                                   |
| 2      | Ledger append-only                                      | **unchanged**                                                                                                                                                                                                                                                           |
| 3      | Every operation idempotent                              | **unchanged internally**; ⚠ the *bank-side* guarantee is weakened (§5.3.1) and must be rebuilt                                                                                                                                                                          |
| 4      | One op per wallet                                       | **unchanged**                                                                                                                                                                                                                                                           |
| 5      | Fixed lock order                                        | **unchanged**                                                                                                                                                                                                                                                           |
| 6      | Sandbox never becomes real                              | **unchanged**                                                                                                                                                                                                                                                           |
| 7      | Real money enters the ring-fence only via `real → game` | **unchanged** — still one internal edge                                                                                                                                                                                                                                 |
| 8      | `real → game` limit counts gross inflow                 | **unchanged**                                                                                                                                                                                                                                                           |
| 9      | `game` is real money                                    | **strengthened** — it is now real money in an account in the user's own name                                                                                                                                                                                            |
| 10     | Consent opt-in and auditable                            | **extended** — the mandate (§9.2) and the Asaas-as-IP disclosure join the addendum; subaccount creation and key rotation become `wallet_audit` events                                                                                                                   |
| 11     | Webhook never the source of truth                       | **preserved** — re-query `GET /v3/payments/{id}`; require `RECEIVED` not `CONFIRMED`; payer CPF now comes from `GET /v3/customers/{id}` instead of the webhook body, which is *better* (Inter's re-query dropped it entirely)                                           |
| 12     | No money in limbo                                       | **preserved and widened** — new limbo states: subaccount `onboarding`/rejected, frozen subaccount (§5.5), custody drift (§5.4)                                                                                                                                          |
| **13** | **NEW: ledger equals custody**                          | `real + game + Σholds + <fees swept but not yet transferred> == subaccount balance` per user; drift alarms. The fee term (§6.4.2) and the settlement term (§5.4.2) are the only legitimate sources of drift, and both are bounded and named — anything else is a defect |
| **14** | **NEW: CTech's own account never holds user money**     | every path that would place user funds in the parent account is a bug, whatever it enables                                                                                                                                                                              |
| **15** | **NEW: CTech is never a counterparty in a game, nor takes a share of a real-money pot** | player-versus-player only; real-money tables rake-free with a fixed entry fee per tier (`ctech-poker` `hand.ConfigureRake` → `rakeBPS = 0`, `stakes.go`). This is the executable form of the skill-game classification (§9.4): a house position, a loss covered by CTech, or a percentage of a real-money pot breaks it, whatever revenue it adds |

---

## 8. Cost model

| Item                                            | Cost                                      | Who bears it                      | Note                                                           |
|-------------------------------------------------|-------------------------------------------|-----------------------------------|----------------------------------------------------------------|
| Subaccount creation                             | R$13,90 one-off                           | CTech                             | lazy creation only (§6.5)                                      |
| Pix received (static QR)                        | R$1,99, ~100 free/month                   | user, via `netValue`              | free-quota scope is **Q1** below                               |
| Pix withdrawal (Asaas tariff)                   | R$1,99, ~30 free/month **per subaccount** | CTech, out of the 2% fee (§6.4.1) | free quota ⇒ the whole fee is margin                           |
| Withdrawal fee (CTech revenue)                  | 2%, min **R$2,50**, max R$10              | user                              | the wallet's only revenue; needs its own transfer leg (§6.4.2) |
| Internal transfer (settlement, entry fees, fee sweep) | **R$0**                                   | —                                 | makes §5.4 and §6.4.2 viable                                   |
| EVP Pix key                                     | R$0                                       | —                                 | 1/min per account rate limit                                   |

---

## 9. Legal implications

Not legal advice. This section is engineering's map of the exposure, for counsel to rule on. The gambling and
tax items need a lawyer and an accountant **before** launch, not after.

### 9.1 What the migration genuinely fixes

- **Commingling** — money is in accounts held by users, at an institution authorized by BCB.
- **Bankruptcy / execution pooling** — user funds are not CTech assets and never enter its estate.
- **Apropriação indébita (CP art. 168)** — the elementary conduct requires possession of another's movable
  property. CTech no longer has it; it has an instructed mandate over accounts belonging to others.
- **Unauthorized payment-institution activity** — Asaas is the authorized institution. CTech's activity moves
  toward technology/platform provider.

This is a large, real improvement. The user's instinct is correct.

### 9.2 What it does NOT fix — and the mandate is #1

**Control is not ownership.** CTech holds every subaccount's API key and moves the users' money unilaterally.
Without express authority that is *worse* than the omnibus model, not better — moving money out of an account
belonging to someone else, without a mandate, is closer to the crime the migration was meant to avoid.

The **contrato de mandato** (CC arts. 653–692) is therefore the load-bearing legal artifact and must be an
express, versioned, audited clause in the wallet addendum. It must grant, and bound:

1. Opening a payment account at Asaas in the user's name and on their behalf.
2. Requesting a Pix key (EVP) and generating collection QR codes on that account.
3. Executing Pix transfers **only** to a key belonging to the user's own verified CPF (withdrawal), and internal
   transfers **only** to settle obligations recorded in the CTech ledger (game settlement, rake).
4. Explicit prohibitions: no third-party destinations, no use of balance for CTech's own account, no credit
   operations.
5. Revocability, and what revocation means operationally (balance returned to the user's own Pix key, account
   closed).

Acceptance must be versioned exactly like the existing terms/gambling addenda
(`CurrentTermsAddendumVersion` pattern — computed equality, never a stored boolean) and appended to
`wallet_audit`. Bumping the mandate version re-gates money movement, and per the existing rule
(three-wallet spec) **must never trap the user's money** — returning funds to the user must always remain
available.

**Can this simply live in the terms of use? Yes — and that is the right vehicle.** A mandate needs no special
form (CC art. 656 — it may be express or tacit, written or verbal), so a clause in the wallet addendum is a valid
grant. Three conditions decide whether it survives a challenge, and all three are engineering-visible:

1. **Specificity.** CC art. 661: a mandate *in general terms* confers only powers of ordinary administration;
   anything beyond that requires express, special powers. A clause saying "podemos administrar sua conta" grants
   nothing useful. It must enumerate the acts of §9.2 items 1–4 individually — open the account, request the Pix
   key, generate QR codes, transfer **only** to the holder's own verified CPF, transfer internally **only** to
   settle obligations recorded in the ledger — and state the prohibitions explicitly.
2. **Prominence.** CDC art. 54 §4: clauses that limit consumer rights must be highlighted. A separate,
   distinctly-presented acceptance step — not a line buried in the general terms — and CDC art. 51 makes a blanket
   authorization voidable as abusive. Specificity plus revocability is what keeps it out of that bucket.
3. **Revocability, with a working exit.** CC art. 682: mandates are revocable. Revocation must have a real
   operational meaning — balance returned to the user's own Pix key, subaccount closed — and that path must
   already be built, not promised (§9.8).

Wording is counsel's call. The engineering requirement is that the *acceptance* is a versioned, audited gate on
money movement, exactly like `GamblingAccepted()` is today.

#### 9.2.1 Review of counsel's draft clause 7 (received 2026-07-29)

The draft satisfies all three conditions above: it enumerates five specific acts, states three express
prohibitions, declares gratuity and revocability, and requires versioned re-acceptance. Substantively it is what
§9.2 asked for. What follows is **not** a critique of the drafting — it is the list of places where the clause and
the built system currently disagree. Every one has to be resolved in one direction or the other, and each is
engineering-visible.

| # | Conflict                                                                                                                                                                                                                                                                                                     | Direction to resolve                                                                                                                                                                                              |
|---|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| A | **Item (v) authorizes "taxas de serviço fixas"** — the withdrawal fee is *ad valorem* (2%, min R$2,50, max R$10, §6.4). A percentage fee is not a fixed fee. The rake is percentual too.                                                                                                                     | Clause must authorize a percentage fee with a disclosed formula and ceiling, or the fee model changes. The clause as written does not authorize what the code does.                                               |
| B | **Prohibition (b)** forbids using the user's balance "para qualquer finalidade própria" — but §6.4.2's fee sweep is literally a transfer from the user's subaccount to CTech's parent account.                                                                                                               | Needs an express carve-out cross-referencing the fee clause. Item (v) authorizes the *débito*; (b) has to not forbid the *transfer* that executes it.                                                             |
| C | **Item (iv) mentions "contas de custódia de torneios"** — §5.4.1 deliberately **rejected** a pot/table/tournament account. If such an account is CTech's, it re-creates the exact omnibus defect this whole migration removes.                                                                               | Either delete the tournament-account power, or specify that it is itself a subaccount held by a third party that is not CTech. Do not leave it as an unused authorization.                                        |
| D | **Item (iii) authorizes Pix only.** If the user's CPF is not registered as a Pix key at any bank, the withdrawal has no destination.                                                                                                                                                                         | Extend to TED/bank transfer to an account of the same verified CPF, or accept that a user without a CPF Pix key cannot withdraw *or be closed out* (which breaks revocation, item F).                             |
| E | **The provider is unnamed** ("provedor de infraestrutura financeira contratado pela CTech"). §9.3: Asaas contractually requires being identified as the *Instituição Prestadora de Serviço de Pagamento*, and the user is being made the holder of an account at a company they were never told the name of. | Name Asaas (with CNPJ) and link its terms. Ask whether a provider swap then needs new acceptance — engineering's answer is that it should.                                                                        |
| F | **Revocation says "devolução integral do saldo disponível"** — *integral* forbids netting the 2% fee out of the closure payout, and *disponível* is silent on held funds.                                                                                                                                    | Decide: closure payout is fee-free (CTech eats the R$1,99 tariff) or the clause says "líquido das taxas". And state that revocation takes effect after pending obligations settle, bounded in time (§5.6 step 1). |
| G | **Revocation is tied to "encerramento da conta"** — full closure. There is no way to revoke the mandate and keep a read-only account.                                                                                                                                                                        | Fine as a product decision, but it means revocation ⇒ destruction. The UI must say so before the confirm, and reopening costs a new R$13,90 (§6.5).                                                               |
| H | **Gratuity vs. item (v).** "O mandato é gratuito" sits next to a clause authorizing service-fee debits.                                                                                                                                                                                                      | Clarify that the fees remunerate the platform's services, not the mandate. Cosmetic, but it is the kind of internal contradiction that gets a clause read against the drafter.                                    |

Two things the clause does not cover at all:

- **Asaas's own contract.** Opening an account in the user's name binds them to a third party's terms. The mandate
  authorizes the opening (item i) but nothing presents or accepts Asaas's terms on the user's behalf, and nothing
  says who does. Ask counsel *and* Asaas.
- **Provider-side block.** §5.5: Asaas can freeze one user's subaccount. In that state CTech cannot return the
  balance either, so revocation becomes impossible through no fault of anyone. Needs to be addressed somewhere —
  and it is a third reason `wallet-terms-addendum.md` §6 ("intermediário técnico de custódia") must be rewritten.

**⚠ Implementation trap, not a legal point.** The draft arrived as a JS object literal with **two `paragraphs`
keys**. `LegalSectionData` in `ctech-account/ui/src/lib/legal-documents.ts:33` is
`{heading, paragraphs: string[], items?: string[]}` — one field. A duplicate key means the second wins and the
first is silently dropped, which in this draft is the sentence that *constitutes the mandate*
(*"…constitui a CTech como sua Mandatária…"*). Merge the two arrays into one before pasting, and note that
`items` renders between them, so the intro paragraph has to be the last element of the array that precedes the
list — i.e. the section needs splitting into two sections, or the ordering has to be checked against
`legal-page-layout.tsx`.

#### 9.2.2 Review of draft v2.2 (received 2026-07-29)

v2.2 resolves conflicts **B, D, E, F, G and H** of §9.2.1, and both gaps (Asaas's terms in item I, provider block
in clause 7). Conflict **C** is resolved by deletion — the tournament custody account is gone and item IV is now
user-to-user only, which is what §5.4 actually builds. The clause is materially sound. What remains:

The corrected text is in **§13**, ready to apply to `ctech-account`
(`ui/src/lib/legal-documents.ts`) once implementation starts. The list below is why each change is there.

**Must fix before publishing**

1. **⚠ Conflict A is resolved the wrong way and has to be re-opened.** v2.2 §3 fixes the withdrawal fee at a flat
   R$2,50 and item V authorizes only fees *"de natureza fixa por operação"*. But the withdrawal fee **stays
   percentage** (§6.4: 2%, min R$2,50, max R$10) — a flat fee collapses the wallet's only revenue on exactly the
   large withdrawals that carry the cost. So the *clause* moves, not the fee: item V must authorize fixed
   per-operation fees **and**, separately, the withdrawal fee calculated as a percentage with a disclosed minimum
   and maximum. Everything else on the platform genuinely is fixed per operation (real-money poker is rake-free with
   a fixed tier entry fee), so this is the single carve-out — name it as one and it reads as precision rather than
   an escape hatch.
2. **⚠ The duplicate `paragraphs` key is still there** — section 7 has two of them again. `LegalSectionData`
   (`ctech-account/ui/src/lib/legal-documents.ts:33`) has one field, so the second array wins and the first is
   dropped: the published clause would lose *"O Usuário, na qualidade de Mandante, constitui a CTech como sua
   Mandatária…"* and start at *"É expressamente vedado…"* — a list of prohibitions with no grant. The `intro`
   mentions the mandate, but a cross-reference is not an outorga. Merge into one array, or split clause 7 into 7
   (grant + items) and 7.1 (prohibitions and conditions), which reads better anyway.
3. **"R$ 2,50 (duzentos e cinquenta centavos)"** — should be *dois reais e cinquenta centavos*. Arithmetically the
   same, but it is a money amount spelled out in a contract; leave nothing for the other side to read as ambiguity.
   Carries over to the R$2,50 **minimum** in the corrected percentage wording.
4. **§6 contradicts itself and clause 7.** First paragraph: *"o saldo disponível deve ser sacado pelo usuário"*
   (burden on the user). Second paragraph and clause 7: CTech transfers it automatically. Keep the automatic
   version only — requiring the user to withdraw first means a user who *cannot* withdraw (case 1 of §6.4.1: dust
   balance below the R$2,50 floor) cannot revoke, which is exactly the "revocation with real effect" test the
   mandate has to pass. The live v2.1 §6 is worse still: it lets residual balances be *"revertidos como receita
   operacional"* after 90 days of inactivity — appropriating a customer's money from an account held in their own
   name, which is the very conduct this migration exists to remove. It must go.
5. **The last paragraph of clause 7 traps money on a version bump:** *"novo aceite expresso do Usuário, sem o qual
   a movimentação financeira permanecerá bloqueada."* Blocking *all* movement conflicts with the standing rule
   (three-wallet spec, and `GamblingAddendumVersion` behaves this way today) that a terms change never freezes a
   user's own money. Carve out **withdrawal to the user's own CPF and account closure**, which must remain
   available without accepting the new version — otherwise a version bump is a lever to hold balances hostage, and
   that reading is what makes the whole clause abusive under CDC art. 51.

**Regressions vs. addendum v1.0 — content that was there and is now gone**

6. **The sandbox non-converti   bility clause vanished.** v1.0 §5 said sandbox *"não é resgatável, não tem valor
   monetário e não pode ser convertido em saldo real ou sacado sob nenhuma hipótese."* v2.2 mentions only "compra
   de créditos de entretenimento" (§4). That sentence is the **contractual expression of Invariant #6** — the thing
   that answers a user who claims their credits are money. Put it back verbatim.
7. **The 18+ requirement vanished.** v1.0 §2 required "18 anos ou mais". Dropping the age gate from a document that
   governs a gambling ring-fence is the wrong direction. (`kyc_level == verified` implies it operationally, but the
   contract should say it.)
8. **v1.0 §6's "intermediário técnico de custódia"** is correctly gone — v2.2 §1 now says the opposite, accurately.
   Confirm the published page actually replaces v1.0 rather than sitting beside it.

**Consistency notes, cheap to fix**

9. **Item V says all fees are "de natureza fixa por operação".** True for real money — real-money poker tables are
   rake-free with a fixed entry fee (`ctech-poker`: `hand.ConfigureRake` → `rakeBPS = 0` for real money;
   `stakes.go` R$1/2/4/8 by tier). But the **sandbox** tables do charge a 2,5% rake (`rakeBPS = 250`). That rake is
   on virtual credits inside `wallets`, never a debit on the Asaas payment account, so item V does not reach it —
   worth one sentence to counsel so nobody later reads item V as prohibiting it.
10. **The Asaas terms URL is the article page** (`central.ajuda.asaas.com/hc/pt-br/articles/32096847160859-…`), not
    `asaas.com/termos-de-uso` as drafted. Fix the link, and make sure the acceptance UI **shows** it — item I has the
    user declare they read Asaas's terms, and CDC art. 46 does not bind a consumer to a document they were not given
    the chance to read. Recording that the link was rendered is the engineering half of that clause.
11. **§2's "deduzidos eventuais custos operacionais"** on third-party refunds — see §5.7: recommend refunding in
    full and absorbing the tariff. Deducting from a stranger's money is a disproportionate fight over R$1,99.
12. **§5 (MED) is new and correct to have** — but it contracts for a mechanism the wallet cannot currently execute
    (§5.7). Do not publish v2.2 without the `med_receivable` path at least designed, or the terms describe a
    capability that does not exist.

### 9.3 Mandatory disclosure of Asaas as the payment institution

Asaas's own BaaS documentation requires the integrator to identify Asaas as the *Instituição Prestadora de
Serviço de Pagamento* (they cite Resolution 16/17 — confirm the exact norm with them, §10 Q5). This is a
contractual obligation, not a nicety: it must appear in the addendum and in the UI. It also finally makes
`docs/legal/wallet-terms-addendum.md` §6 honest — that paragraph's claim of being a *"intermediário técnico de
custódia"* must be rewritten to say CTech custodies nothing.

### 9.4 The skill-game layer — the classification is sound; the risk is misclassification, not illegality

**Poker and dominó are games of skill.** They are not `jogo de azar` and not a `contravenção penal`, and the
exclusion is in the text of the offence itself, not read into it: `Decreto-Lei 3.688/41 art. 50 §3, a` defines
`jogo de azar` as the game in which *"o ganho e a perda dependem exclusiva ou principalmente da sorte"*. A game
whose outcome is predominantly determined by skill falls outside that definition — there is no exemption to
invoke because the conduct was never described. Jurisprudence and the sport recognition of poker reinforce the
classification; they are not what creates it.

Three structural facts of *this* system are what keep the classification defensible, and all three are
engineering-visible — which is why they are now **Invariant #15** (§7):

1. **CTech is never a counterparty.** Every real-money game is player versus player. The house never takes a
   position, never covers a loss, never wins. There is no `banca` to explore.
2. **No share of a real-money pot.** Real-money tables are rake-free (`ctech-poker` `hand.ConfigureRake` →
   `rakeBPS = 0`); the platform charges a **fixed entry fee per tier** (R$1/2/4/8, `api/internal/api/v1/stakes.go`).
   A fixed price for access to a service is a service price; a percentage of the pot is a share in the game's
   result, and it is the single fact that most invites an `exploração de jogo` reading. It is already absent —
   the requirement is to keep it absent.
3. **Fixed-odds betting is a different product and the platform does not offer it.** `Lei 14.790/2023` regulates
   `apostas de quota fixa` — a bettor staking against an *operator* on the outcome of a real event, the operator
   being the counterparty and setting the quota. Player-versus-player skill games with a fixed entry fee have no
   operator counterparty and no quota, so they are outside its object, and its licensing regime (authorization fee
   in the tens of millions) does not reach them.

**Vocabulary is part of the legal position, and the codebase currently works against it.** `art. 50 §3, c` treats
as `jogo de azar` *"as apostas sobre qualquer outra competição esportiva"* — betting **on** a competition. A
player paying an entry fee to **compete** is not betting on anything, and that distinction —
participation versus wager — is carried entirely by the words used. So published text must never say `aposta`,
`apostar`, `apostador`, `odds` or `casa`: use `entrada`, `mesa`, `partida`, `premiação`, `participante`.
Internally the code says gambling everywhere (`GAMBLING_ENABLED`, `gambling/self-exclude`,
`GamblingAddendumVersion`, "gambling ring-fence" in the root `CLAUDE.md`). A private identifier is not an
admission and renaming it is not urgent — but every **user-visible** string and every contract clause is, and a
supplier whose own terms of use call its product gambling has handed the other side an admission nobody asked it
for. The existing choice of `'wallet-gaming'` as the `LegalDocumentId` is the correct pattern; apply it wherever
a user can read.

**What the residual risks actually are:**

- **Provider misclassification, amplified by the migration.** The exposure is not that the activity is unlawful —
  it is that Asaas's compliance team files it as betting. The consequence is no longer "our account is frozen" but
  "**thousands of user accounts are frozen**", each one a consumer with a direct claim. Disclose the activity to
  the account manager **in writing, before onboarding real users** (§10 Q4), and send the *basis*, not just the
  label: player-versus-player, no house counterparty, rake-free, fixed entry fee per tier. Get the classification
  back in writing. Do not discover this at scale.
- **Regulatory evaluation period is a hard launch gate** (§10 Q3): max 10 subaccounts per distinct holder,
  R$2.000 per subaccount, 60 days from the first subaccount, after which *creation is automatically blocked*.
  A public launch that outruns evaluation clearance stops mid-onboarding with users stranded in `onboarding`
  state. **This gate is on the wallet, not on poker** — the caps are on subaccount creation and per-subaccount
  volume, so deferring the poker launch does not defer it. Clear it before the first public wallet signup.

The responsible-gaming machinery is unaffected and should stay exactly as it is: `real → game` is still one
internal edge, still the only door, still metered gross, and Invariant #7 survives the custody change intact.
Personal limits and self-exclusion are the strongest available evidence that the operator behaves responsibly and
they cost nothing legally — they are `jogo responsável`, not a concession that the game is `jogo de azar`.

### 9.5 PLD/AML — the platform layer is where the risk now lives

Asaas performs KYC per subaccount, which genuinely offloads identity risk. Two structural facts help further:

- **There is exactly one exit.** BaaS subaccount holders have no Asaas panel and no key (§5.4), so no value
  leaves without passing `POST /wallet/withdrawals`. A single choke point is the ideal position for monitoring —
  far better than the omnibus model, where the money could also leave by CTech's own hand.
- **Withdrawals are same-CPF-only**, already enforced (`pixKey = kyc.CPF`, client never supplies a destination).

Neither fact offloads **transaction monitoring**, and the topology still contains a textbook laundering channel:
A deposits, A "loses" to B at a private table, B withdraws — clean money, complete paper trail, every leg executed
on CTech's instruction. Having the only exit means CTech is the only party who *can* detect it, which makes the
duty ours rather than Asaas's. Minimum controls: settlement-pattern monitoring between recurring pairs of users
(the settlement batch of §5.4.3 is the natural detection surface — it names both sides and the amount),
velocity limits, a documented suspicious-activity policy, and retention of the audit trail.

Be precise about *which* duty this is. Since the platform is not a fixed-odds betting operator (§9.4), the PLD
obligation here is primarily **contractual** — Asaas is an obliged party under `Lei 9.613/98` and its BaaS
contract passes those duties down to the integrator — plus the ordinary criminal exposure for willful blindness
(`Lei 9.613/98 art. 1`). It is not a formal COAF-registration duty, and it is not smaller for that: contractual
breach is precisely what gets thousands of subaccounts closed at once (§9.4).

### 9.6 Tax and invoicing

- **Table entry fees** (fixed per tier) = CTech revenue → invoice and tax as *serviço de tecnologia/plataforma*.
  There is no rake on real money (§9.4), so no revenue line is a share of a pot — which is also the cleaner tax
  characterization: a price for access to a service, not participation in a result.
- **Withdrawal fee (2%)** = CTech revenue → **invoice and tax it**, as a *serviço de carteira digital / tecnologia*
  (§6.4.1's naming rule is a tax classification decision too, not only a legal-risk one). It reaches a company
  account by the sweep leg of §6.4.2 — which is precisely what makes it clean, auditable revenue rather than money
  resting in a customer's account. The Asaas tariff it absorbs is a deductible cost, not a reduction of revenue:
  gross fee in, tariff out.
- **Deposit Pix tariffs** = Asaas's charge to the user's own account → not CTech revenue and **not** to be
  invoiced by CTech (§6.2).
- **Prize/winnings taxation** for skill games between players needs an accountant before launch, and the
  structure helps: the IRRF rules on `prêmios` were written for prizes *paid by an organizer* (sweepstakes,
  concursos). Here CTech pays no prize — losing players' balances move to winning players' balances, netted, and
  CTech is never a source of payment (Invariant #15). That is an argument against a withholding duty on CTech, not
  a conclusion. Get it in writing.
- **User-to-user settlements** are not CTech revenue but pass through CTech's instruction — keep them
  distinguishable in the ledger so an audit can see the difference at a glance. The ledger entry types already
  do this; keep it that way.

### 9.7 LGPD

Full name, CPF, birth date, address, phone, income, and identity documents are transmitted to Asaas, who then
runs its own KYC (selfie + ID) directly with the user. Required: legal basis (art. 7º V — contract execution),
Asaas named in the privacy policy with its role (operator for account opening, controller for its own KYC),
DPO contact already present (`dpo@aoctech.app`), and care with the selfie/document flow. The `onboardingUrl`
path is preferable here too: the user submits documents **to Asaas directly**, so CTech never stores them.

A deletion request (art. 18 VI) does **not** reach `ledger_entries` or `wallet_audit`: retention there rests on
legal obligation (art. 16 I) and on the append-only invariants, and account closure (§5.6) is not erasure. State
that in the privacy policy instead of discovering it when the first request arrives.

### 9.8 Succession, closure, and the user's right to leave

The account is in the user's name. On death, succession law applies to the balance — CTech cannot simply move it.
On request, the user must be able to close and take their money out. Both need a documented process; neither
existed under the omnibus model because the question could not even be asked.

### 9.9 Illegality review of the drafted text (2026-07-29)

Second pass over the whole document, looking specifically for clauses and mechanics that are **unlawful or void**,
not merely risky. The skill-game classification is taken as settled (§9.4), so everything below is consumer,
custody or payment law. Ordered by how likely a clause is to be struck.

**(a) The liability exclusion in §13 clause 9 is void as drafted.** *"não se responsabiliza por atrasos
decorrentes de exigências regulatórias ou medidas de compliance do provedor"* — CDC **art. 51 I** voids any clause
that exonerates or attenuates the supplier's liability toward a consumer, and **art. 14** makes a service supplier
objectively liable. CTech chose the provider; the consumer did not. The clause also buys nothing, because a
best-efforts-plus-information duty is what would be left standing anyway. Rewritten to promise notice, effort and
progress updates, with an express statement that CDC liability is not excluded.

**(b) The blanket no-refund on sandbox credits collides with CDC art. 49 — and the fix touches Invariant #6.
DECISION NEEDED.** *"A sua aquisição é definitiva e não gera direito a reembolso em dinheiro"* cannot survive
**art. 49**: an online purchase carries a 7-day right of regret that a contract cannot waive, and Brazilian law
has no digital-content exception to it. Credits already played are a service rendered and are not refundable — but
**unused** credits inside 7 days are. Honouring that requires a bounded `sandbox → game` reversal, and Invariant
#6 says *nothing* converts sandbox back. Engineering's reading: the invariant's purpose — sandbox never acquires
monetary value and never becomes an exit path — survives a reversal that is capped at the purchase amount, limited
to unused credits, limited to 7 days, and returns the price to `game` (the wallet it was debited from), never to
`real` and never toward a Pix key. It cannot move value between users, and it grants no `real → game` headroom, so
Invariant #8 is untouched: the money already passed the meter on the way in. **This is the only finding here that
cannot be implemented without amending an invariant, so it needs explicit sign-off rather than a quiet patch.**
Until it exists, §13 carries the art. 49 wording and the code does not honour it — so that clause must not be
published before the reversal path ships.

**(c) Suspending all withdrawals over a `med_receivable` contradicts the mandate's own carve-out, and reads as
retention.** §13 clause 6 ¶2 suspended `saque` until the debt was paid; clause 9 ¶8 promises withdrawal and
closure are *always* available. Both cannot be true. Beyond the internal contradiction, holding a consumer's
entire balance to secure a claim, with no judicial step and no ceiling, is `retenção`, and CC **art. 368–369**
authorizes `compensação` only up to the amount of the debt — not across the whole account. Fixed: the receivable
is compensated against future credits and blocks funding games, the balance **above** it stays withdrawable, and
it never blocks closure. Read §5.7 step 3 with that limit.

**(d) Writing off a closing user's residual balance to CTech is appropriation, whatever it is called.** The
earlier §13 clause 7 booked un-transferable residue as *"custo operacional da CTech"*. If those centavos
physically move to the parent account, that is the same conduct as v2.1's 90-day *"revertidos como receita
operacional"* clause this migration exists to delete (§9.2.2 #4) — smaller, and better dressed. It also breaks
Invariant #14. Fixed by making the closure payout **fee-free and CTech-funded**: when the balance is below the
transfer cost, the parent tops the subaccount up so the user receives 100%. Cents per closed account, and the
whole branch disappears (§6.4.1, §5.6 step 4).

**(e) `min_withdrawal` without a full-balance carve-out traps money.** A R$25 minimum means a user holding R$20
cannot reach their own money except by destroying the account — a restriction on access to one's own funds (CDC
art. 51 IV), and the dust trap of §6.4.1 case 1 one order of magnitude up. Rule added: the minimum never applies
when the requested amount is the entire available balance.

**(f) Open-ended discretionary blocking is an abusive clause.** §13 clause 6 ¶1 allowed blocking for *"suspeita de
fraude, erro operacional"* with no duration and no review path. A block ordered by an authority or required by the
provider is outside CTech's control and needs no bound; a block **CTech chooses** does. Added: reason and contest
channel in the notice, and a 10-business-day review after which the balance is released if the suspicion is not
confirmed.

**(g) The mandate is not `com destaque` where §13 puts it.** §9.2 condition 2 requires a separate,
distinctly-presented acceptance step — CDC **art. 54 §4**: clauses limiting consumer rights must be highlighted
for immediate and easy comprehension. §13 places the mandate as sections 8–9 *inside* the wallet addendum, the
same document as the fee table. The gambling addendum already demonstrates the right pattern: its own
`LegalDocumentId`, its own acceptance, its own version gate. Recommendation: split sections 8–9 into
`'wallet-mandate'` with a `CurrentMandateVersion` separate from `CurrentTermsAddendumVersion`. That also fixes a
versioning defect — §9.2 requires a *mandate* bump to re-gate money movement, which should not fire every time a
fee line is edited.

**(h) A mandate to accept the provider's *future* terms is broader than CC art. 661 supports.** §13 clause 8
item I authorizes accepting Asaas's terms in the user's name with the full text shown at acceptance — correct,
and CDC **art. 46** demands exactly that. But provider terms change. An open authorization to bind the mandante to
later versions is the `mandato em termos gerais` **art. 661** confines to ordinary administration, and the sort of
blanket power art. 51 IV reaches. Added to clause 9: material changes are communicated, and the mandate does not
authorize accepting conditions that widen the user's obligations toward the provider.

**(i) The withdrawal fee's *basis* belongs in the contract, not only its number.** An `ad valorem` charge levied
at the moment of a transfer is economically indistinguishable from a payment tariff, and only an authorized
institution may charge for a payment service (`Lei 12.865/2013`). §6.4's naming rule is right but thin standing
alone. §13 clause 4 now states *what the percentage is for* — account maintenance, ledger, reconciliation,
monitoring and support, services whose cost and risk scale with the value held and moved — and names the partner
as the provider of the payment service itself. A percentage with a stated basis is a service price; a percentage
with no stated basis reads as a tariff.

**Checked and clean:** same-CPF-only deposits and withdrawals; refunding third parties in full (§5.7); crediting
`netValue` (§5.2.1); no CTech-billed payment fee (§6.2); absorbing the R$13,90 (§6.5); the death clause
(CC art. 682 IV); the 18+ gate; sandbox non-convertibility as the contractual form of Invariant #6; disclosure of
Asaas as the payment institution (§9.3); deposit and withdrawal limits shown before the operation; and the
append-only `wallet_audit` with its IAM deny on `UpdateItem`/`DeleteItem`.

---

## 10. Open questions — Asaas commercial/support, before implementation

1. ✅ **ANSWERED (2026-07-29): the free-Pix quota is per subaccount** — each subaccount has its own quota and its
   own management. Consequence: deposits and withdrawals are free for ordinary users; §6 becomes a guard for the
   tail, not the default experience; `min_deposit` needs no change; and the free quota becomes pure margin on the
   2% withdrawal fee (§6.4.1) rather than a number the ledger has to predict.
2. ✅ **ANSWERED: BaaS subaccount holders have no panel/login/key access** — every movement is executed by the
   parent via API. This is the fact §5.4 rests on. Get it restated in writing for the compliance file.
3. **Regulatory evaluation period:** exact limits for our account and how to clear it (§9.4). ⚠ Note this gates
   the **wallet** launch, not just poker: the caps are on *subaccount creation* (max 10 per distinct holder) and
   *R$2.000 per subaccount* over 60 days. Deferring poker does not defer this.
4. **Is the activity (poker/dominó for rake, real money) accepted?** In writing, from the account manager.
5. **BaaS subaccount `apiKey`:** confirmed returned at creation for BaaS clients, and rotation available without
   the 2-hour manual UI enablement?
6. **Exact norm** behind the "identify Asaas as Instituição Prestadora de Serviço de Pagamento" requirement.
7. **Does a subaccount Pix transfer ever require SMS authorization** (`authorized: false`)? If so, the automated
   withdrawal path is blocked and needs a different mechanism.
8. **Static QR limits** per subaccount (count, rate) — undocumented; the per-deposit QR strategy depends on it.
9. **Do subaccount events reach the parent's webhook**, or must webhooks be registered per subaccount (via the
   `webhooks` array at creation)? Affects the onboarding call shape.
10. **Internal transfer between two *sibling* subaccounts** — confirmed supported (§5.4), but is there a rate
    limit or a daily cap? A busy table close emits up to `N-1` legs at once.
11. **Escrow account** (`Conta Escrow`) — does it offer anything for in-play funds that §5.4's internal transfers
    do not, and at what per-subaccount fee?
12. **MED:** which webhook event announces a `Mecanismo Especial de Devolução` reversal on a subaccount, what
    notification window we get before the debit settles, and whether it can be contested with evidence (§5.7).
    Referenced by §5.7 step 1 — the whole `med_receivable` design depends on not learning about MED from a
    balance mismatch.

---

## 11. Phasing

1. **Custody first, no gambling.** Onboarding + deposit + withdrawal on subaccounts; `GAMBLING_ENABLED=false`
   (already the default). Invariant #13 reconciliation job lands with it. Legal artifacts (§9.2, §9.3) published
   and gated before the first real user.
2. **Settlement.** §5.4 internal transfers, rake to parent, `settleCustody`, drift alarms. Then games.
3. **Decommission Inter.** Only after every pending deposit and `processing` withdrawal at Inter is resolved and
   the Inter balance is zero. Both integrations must coexist during migration — a spec of its own, including
   what happens to existing users whose money currently sits in the Inter omnibus account (they must be
   onboarded to a subaccount and their balance transferred **to their own account**, one at a time, audited).

## 12. Non-goals

- No change to the ledger, idempotency, locking, or three-wallet topology.
- No change to the responsible-gambling limit engine or the `real → game` choke point.
- No pot/omnibus account for tables (§5.4) — explicitly rejected.
- No CTech-billed payment fee (§6.2) — explicitly rejected.
- No migration mechanics for existing Inter balances (separate spec, §11.3).

---

## 13. Appendix — corrected wallet addendum v2.2, ready to apply

Counsel's draft with every fix from §9.2.2 folded in. **Not applied yet** — it lands in `ctech-account`
(`ui/src/lib/legal-documents.ts`) when implementation starts, not before.

**Where it goes.** `legalDocuments.wallet` currently holds **v2.1** (`updatedAt: '25 de julho de 2026'`). Applying
this means:

1. Archive the live 2.1 as a new `LegalDocumentId` — `'wallet-v2-1'` — with a page at
   `ui/src/app/products/wallet/v2-1/page.tsx` following the existing one-liner pattern of
   `products/wallet/v2/page.tsx`.
2. Prepend `{version: '2.2', updatedAt: '29 de julho de 2026', href: '/products/wallet'}` to
   `WALLET_VERSION_HISTORY` (`ui/src/components/legal-page-layout.tsx:30`) and repoint 2.1 at
   `/products/wallet/v2-1`.
3. Replace `legalDocuments.wallet` with the object below.
4. Bump `CurrentTermsAddendumVersion` in the wallet API and re-gate acceptance — with the carve-out of §9.2.2 item 5
   (withdrawal to own CPF and closure stay available without accepting).

**Fixes applied:** item V split so the percentage withdrawal fee is authorized (§9.2.2 #1); single `paragraphs`
array, clause 7 split into 7 and 7.1 so the grant survives rendering (#2); *dois reais e cinquenta centavos* (#3);
§6 automatic payout, 90-day revenue-reversion clause deleted (#4); version-bump carve-out (#5); sandbox
non-convertibility restored (#6); 18+ restored (#7); sandbox rake carved out of item V (#9); correct Asaas terms URL
(#10); refunds in full (#11); MED wording aligned to the `med_receivable` path of §5.7 (#12); fee-free closure
payout and `min_withdrawal` with a full-balance carve-out (§6.4.1).

**Plus the §9.9 illegality pass**, which changed clause text again: the liability exclusion in clause 9 is gone
(a); the sandbox purchase now honours CDC art. 49 for unused credits (b — **this one needs an Invariant #6
decision before it can be published**); the MED receivable no longer blocks withdrawing the balance above it, nor
closure (c); the residue write-off is gone and the closure payout is CTech-funded (d); the full-balance carve-out
on `min_withdrawal` is in clause 4 (e); discretionary blocks are bounded at 10 business days with a contest
channel (f); the mandate does not authorize accepting *future* provider terms that widen the user's obligations
(h); and clause 4 now states the *basis* of the percentage fee, not only its number (i).

**⚠ Structural recommendation not applied to the object below (§9.9 (g)).** §9.2 condition 2 requires the mandate
to be accepted in a separate, distinctly-presented step (CDC art. 54 §4), and sections 8–9 sit inside the wallet
addendum here. Recommendation: publish them as their own `LegalDocumentId` (`'wallet-mandate'`) with a
`CurrentMandateVersion` distinct from `CurrentTermsAddendumVersion` — the pattern the gambling addendum already
uses. Left as-is in the object so counsel's document structure is not changed unilaterally.

```ts
  wallet: {
    title: 'Termos Adicionais — CTech Wallet',
        description: 'Condições de saldo, Pix, saques, pagamentos internos e mandato.',
        version: '2.2',
        updatedAt
:
    '29 de julho de 2026',
        versions
:
    WALLET_VERSION_HISTORY,
        intro
:
    'A Wallet é uma carteira digital que opera por meio de parceiro de infraestrutura financeira regulado pelo Banco Central (BaaS). Cada usuário possui conta de pagamento individual e segregada. A CTech não é instituição financeira e a Wallet não constitui conta bancária. Ao utilizar os serviços financeiros, o usuário constitui a CTech como sua mandatária para administração da conta de pagamento, nos termos das seções 8 e 9 deste aditivo.',
        sections
:
    [
        {
            heading: '1. Contas individuais e segregação',
            paragraphs: [
                'Cada usuário com identidade verificada possui uma conta de pagamento digital individual junto ao parceiro financeiro, vinculada ao seu CPF. Os recursos do usuário são mantidos segregados do patrimônio da CTech e dos demais usuários.',
                'O saldo não constitui depósito bancário, investimento, crédito ou rendimento. Não há incidência de juros, correção monetária ou qualquer remuneração sobre o saldo.'
            ]
        },
        {
            heading: '2. Requisitos de acesso',
            paragraphs: [
                'A utilização dos serviços financeiros da Wallet exige idade mínima de 18 anos e verificação de identidade concluída (KYC verificado), conforme as regras do CTech Account e a Política de Verificação de Identidade.',
                'A abertura da conta de pagamento está sujeita à análise e aprovação do parceiro financeiro, que realiza verificação própria. A recusa pelo parceiro impede a utilização dos serviços financeiros, independentemente da verificação já realizada na CTech.'
            ]
        },
        {
            heading: '3. Depósitos e identificação',
            paragraphs: [
                'Depósitos são realizados exclusivamente por Pix para a conta digital individual do usuário. O valor somente é creditado após confirmação pelo parceiro financeiro e conciliação automática, nunca com base isolada em notificação recebida.',
                'O CPF do pagador deve coincidir com o CPF verificado na conta CTech. Depósitos de terceiros são recusados e devolvidos integralmente à origem, sem qualquer dedução, cabendo à CTech os custos operacionais da devolução.',
                'O valor mínimo e o valor máximo de depósito por operação são exibidos antes da geração da cobrança.'
            ]
        },
        {
            heading: '4. Saques e tarifa de serviço',
            paragraphs: [
                'Saques são realizados por Pix a partir da conta digital individual do usuário. A chave Pix de destino deve pertencer ao mesmo CPF verificado na conta. Na ausência de chave Pix válida, poderá ser utilizada transferência para conta bancária de mesma titularidade via TED ou outro meio disponível, conforme autorizado na seção 8.',
                'A tarifa de serviço da carteira é de 2% (dois por cento) sobre o valor sacado, com mínimo de R$ 2,50 (dois reais e cinquenta centavos) e máximo de R$ 10,00 (dez reais) por operação, debitada do saldo do usuário no momento do saque. A tarifa remunera a manutenção da conta na plataforma, o registro em livro-razão, a conciliação, o monitoramento e o suporte — serviços cujo custo e risco variam conforme o valor mantido e movimentado, razão pela qual a tarifa é calculada em percentual, com valor mínimo e máximo — e cobre a tarifa de transferência cobrada pelo parceiro financeiro. Não se trata de cobrança pelo serviço de pagamento em si, que é prestado pelo parceiro financeiro autorizado pelo Banco Central.',
                'O valor mínimo de saque e a tarifa aplicável são exibidos antes da confirmação de cada operação. O valor mínimo de saque não se aplica ao saque da totalidade do saldo disponível, que permanece sempre acessível ao usuário. O saque decorrente do encerramento da conta é integral e isento de tarifa, nos termos da seção 7.',
                'Os valores desta seção podem ser atualizados mediante nova versão deste aditivo e novo aceite do usuário.'
            ]
        },
        {
            heading: '5. Pagamentos internos e créditos de entretenimento',
            paragraphs: [
                'O saldo pode ser utilizado para pagar serviços integrados ao ecossistema CTech, incluindo taxas fixas de criação ou entrada em mesas, compra de créditos de entretenimento e demais serviços ofertados, sempre com o valor informado antes da confirmação.',
                'As movimentações internas entre contas de usuários para liquidação de obrigações de jogo (buy-in e premiações) são processadas diretamente pelo parceiro financeiro, conforme autorizado na seção 8, e registradas no extrato do usuário.',
                'Os créditos de entretenimento (sandbox) são moeda virtual sem valor monetário: não são resgatáveis, não rendem, não podem ser convertidos em saldo real e não podem ser sacados sob nenhuma hipótese.',
                'Ressalvado o direito de arrependimento previsto no artigo 49 do Código de Defesa do Consumidor, exercível no prazo de 7 (sete) dias contados da compra e limitado aos créditos não utilizados — caso em que o valor pago é estornado ao saldo de origem na própria carteira —, a aquisição de créditos de entretenimento é definitiva. Créditos já utilizados em partidas correspondem a serviço prestado e não são reembolsáveis.'
            ]
        },
        {
            heading: '6. MED, bloqueios e retenções',
            paragraphs: [
                'Operações podem ser bloqueadas, devolvidas ou ajustadas em razão do Mecanismo Especial de Devolução (MED), ordem judicial ou administrativa, suspeita de fraude, erro operacional ou obrigação regulatória do parceiro financeiro.',
                'Caso um valor já creditado seja devolvido por determinação do MED ou de autoridade competente e o saldo do usuário não seja suficiente para suportar a devolução, o valor remanescente constituirá obrigação do usuário perante a CTech, registrada em seu extrato e informada ao usuário com indicação da operação que a originou. Enquanto não quitada, a obrigação é compensada com créditos futuros e a movimentação de saldo para jogos permanece suspensa. O saque e o encerramento da conta permanecem disponíveis quanto ao saldo que exceder o valor da obrigação, e a existência da obrigação não impede o encerramento da conta.',
                'A CTech preservará evidências e, quando permitido por lei ou pelo regulador, notificará o usuário sobre a medida aplicada, com indicação do motivo e do canal para contestação. Bloqueios motivados por suspeita de fraude ou erro operacional, quando não decorrentes de ordem de autoridade competente ou de exigência do parceiro financeiro, serão revistos em até 10 (dez) dias úteis, findo o prazo sem confirmação da suspeita o saldo é liberado.'
            ]
        },
        {
            heading: '7. Encerramento e devolução de saldo',
            paragraphs: [
                'O encerramento da conta pode ser solicitado a qualquer tempo pelo usuário e será processado após a liquidação de obrigações pendentes, como partidas em andamento, em prazo informado ao usuário.',
                'Concluída a liquidação, a CTech transferirá o saldo disponível para chave Pix ou conta bancária de titularidade do usuário, nos termos da seção 8, e encerrará a conta de pagamento. Não é exigido que o usuário realize o saque previamente.',
                'A devolução do saldo no encerramento é integral e isenta de tarifa de saque. Os custos da transferência são suportados pela CTech, inclusive quando o saldo disponível for inferior a esses custos, hipótese em que a CTech aporta a diferença para que o usuário receba a totalidade do seu saldo. Em nenhuma hipótese saldo remanescente do usuário é apropriado pela CTech ou convertido em sua receita.',
                'A reabertura da conta após o encerramento implica nova conta de pagamento, novo custo de abertura e novo aceite deste aditivo.'
            ]
        },
        {
            heading: '8. Autorização para Administração de Conta de Pagamento (Mandato)',
            paragraphs: [
                'O Usuário, na qualidade de Mandante, constitui a CTech como sua Mandatária, nos termos dos artigos 653 a 692 do Código Civil, em caráter especial e limitado, exclusivamente para a prática dos seguintes atos:'
            ],
            items: [
                'I – Abertura de conta de pagamento individual no provedor de infraestrutura financeira contratado pela CTech, em nome e no interesse do Usuário, e aceitação, em nome do Usuário, dos termos de uso e de abertura de conta do provedor, cujo texto integral é apresentado ao Usuário no momento do aceite deste aditivo;',
                'II – Solicitação de chave Pix (EVP) e geração de QR Codes de cobrança vinculados à referida conta;',
                'III – Execução de transferências para conta de mesma titularidade do Usuário, seja via Pix (chave de titularidade do mesmo CPF verificado) ou, na ausência de chave Pix válida, via TED ou outro meio disponível para conta bancária de mesma titularidade;',
                'IV – Realização de transferências internas entre a conta do Usuário e as contas de outros Usuários, exclusivamente para liquidação de obrigações de jogo registradas no livro-razão da CTech (buy-in e premiações), inclusive de forma compensada entre as posições líquidas dos participantes de uma mesma mesa;',
                'V – Débito das taxas de serviço devidas pela utilização da plataforma, a saber: (a) taxas fixas por operação, informadas antes de cada operação, incluindo taxas de criação e de entrada em mesas; e (b) a tarifa de serviço da carteira incidente sobre saques, calculada em percentual sobre o valor sacado, com valor mínimo e máximo, nos termos da seção 4;',
                'VI – Transferência das taxas e tarifas referidas no item V para conta de titularidade da CTech.'
            ]
        },
        {
            heading: '9. Limites, provedor e revogação do mandato',
            paragraphs: [
                'É expressamente vedado à CTech, na qualidade de Mandatária: (a) transferir recursos para terceiros diversos do Usuário titular, salvo para liquidação das obrigações de jogo previstas no item IV da seção 8; (b) utilizar o saldo da conta do Usuário para qualquer finalidade própria, exceto o débito das taxas e tarifas autorizadas no item V e a sua transferência nos termos do item VI; (c) realizar operações de crédito ou qualquer atividade não listada na seção 8.',
                'A remuneração da plataforma limita-se às taxas e tarifas do item V. O mandato em si é outorgado em caráter gratuito, sem remuneração específica pela administração da conta.',
                'As taxas e tarifas do item V não alcançam os créditos de entretenimento (sandbox), que não transitam pela conta de pagamento: eventuais taxas incidentes sobre partidas em créditos de entretenimento são cobradas exclusivamente em créditos, sem qualquer débito na conta de pagamento do Usuário.',
                'Provedor atual: Asaas Gestão Financeira Instituição de Pagamento S.A., inscrita no CNPJ sob o nº 19.540.550/0001-21, cujos Termos e Condições de Uso podem ser consultados em https://central.ajuda.asaas.com/hc/pt-br/articles/32096847160859-Termos-e-Condi%C3%A7%C3%B5es-de-Uso. A substituição do provedor implicará nova versão deste mandato e exigirá novo aceite expresso do Usuário. Alterações relevantes nos termos do provedor serão comunicadas ao Usuário, e este mandato não autoriza a CTech a aceitar, em nome do Usuário, condições que ampliem as obrigações por ele assumidas perante o provedor.',
                'O mandato é revogável a qualquer tempo pelo Usuário, mediante solicitação de encerramento da conta, com os efeitos previstos na seção 7.',
                'Caso a conta do Usuário seja bloqueada ou sofra restrições pelo provedor de pagamento, a CTech informará o Usuário, empregará seus melhores esforços para a regularização e a liberação dos valores e o manterá informado sobre o andamento. A escolha do provedor é da CTech, e esta cláusula não exclui nem limita a responsabilidade da CTech nos termos do Código de Defesa do Consumidor.',
                'O mandato extingue-se automaticamente pela morte do Usuário. A partir da ciência do óbito, a CTech interromperá as movimentações e aguardará instruções dos herdeiros, observada a legislação sucessória.',
                'A aceitação desta cláusula é registrada de forma versionada. Qualquer alteração nos poderes aqui conferidos implicará nova versão do termo e exigirá novo aceite expresso do Usuário, sem o qual as movimentações financeiras permanecerão bloqueadas — ressalvados, em qualquer hipótese, o saque para chave Pix ou conta bancária de titularidade do próprio Usuário e o encerramento da conta com devolução do saldo, que permanecem sempre disponíveis.'
            ]
        },
    ],
}
,
```

**Two things this text promises that the code does not yet do**, and they must ship together with it — publishing
the addendum first would describe capabilities that do not exist:

| Clause            | Requires                                                      |
|-------------------|---------------------------------------------------------------|
| §7 (encerramento) | `POST /v1.0/wallet/closure` — §5.6. **Does not exist today.** |
| §6 ¶2 (MED)       | the `med_receivable` path — §5.7. **Does not exist today.**   |

Also gated on it, but smaller: `min_withdrawal` with its full-balance carve-out (§6.4.1), the fee-free
CTech-funded closure payout, `FEE_ABSOLUTE_MIN = 250`, the 10-business-day review on discretionary blocks, and
rendering the Asaas terms at the acceptance step with an audit record that they were shown (§9.2.2 #10).

And one clause that **cannot ship without an invariant decision**: §5's CDC art. 49 refund of unused sandbox
credits needs a bounded `sandbox → game` reversal, which Invariant #6 currently forbids outright. §9.9 (b) states
the case; it is the only item in this appendix that engineering must not resolve on its own.
