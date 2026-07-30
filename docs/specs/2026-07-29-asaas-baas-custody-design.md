# Asaas BaaS + Per-User Subaccounts — Custody Redesign

**Status:** **BaaS custody premises confirmed; real-money games remain under LEGAL HOLD**
**Date:** 2026-07-29
**Legal review:** 2026-07-30 (preventive engineering review; requires Brazilian counsel's signed opinion)
**Supersedes:** `docs/specs/2026-07-13-pix-gateway-lambda-design.md` (Inter transport),
`docs/specs/2026-07-13-inter-token-manager-design.md` (dead once Inter is gone)
**Amends:** `docs/specs/2026-07-12-three-wallet-topology-design.md` (custody layer only — the three-wallet ledger model
is unchanged)
**Blocks:** production launch of real-money games until the independent games-law blockers in §§0.2 and 14 are
resolved. Asaas BaaS account/API enablement is no longer an open blocker under the confirmed premises in §0.1.

---

## 0. Legal hold and rules that override the rest of this document

This specification is a technical design, **not a legal opinion**. Statements below that describe a legal
classification are hypotheses for counsel, not authorization to operate. The Asaas BaaS account/API premises are
confirmed in §0.1. Fees, user-to-user game settlement and paid games remain subject to the specific unresolved
blockers identified in this document and to Brazilian counsel experienced in payments, consumer law, PLD/FTP
and games.

The original draft pre-dated the regulation that now governs this model. The controlling BaaS rule is
**Resolução Conjunta BCB/CMN nº 16/2025**, in force since 28 November 2025. In particular:

1. The end user must have a direct contractual relationship with the BaaS institution for the financial and
   payment services, while the user contracts with CTech only for the other services
   (**Res. Conj. 16/2025, art. 3º, IV**).
2. The payment account must be held by the end user at the BaaS institution
   (**art. 4º, §2º**). This must be proven by the executed Asaas contract and account records; calling an API
   object a `subaccount` is not proof of legal ownership.
3. CTech may not charge, **in its own name**, a tariff, commission or other remuneration for products or
   services offered by Asaas (**art. 8º, XI**), and may not receive in its own account values related to those
   services (**art. 8º, XIV**). Consequently, the proposed 2% withdrawal charge and its sweep to CTech are
   **disabled launch blockers**. Renaming a withdrawal charge as a “platform service fee” does not change its
   legal substance. It may be implemented only if the executed BaaS contract, Asaas compliance and counsel all
   confirm in writing that it pays for a genuinely separate, non-financial CTech service and complies with
   arts. 8º, XI, and 15. Otherwise Asaas must charge any permitted payment tariff in its own name.
4. The UI and contracts must identify Asaas visibly as the regulated provider, state that CTech is not an
   institution authorized by BCB, and must not imply that CTech supplies the financial service
   (**arts. 8º, §2º, I–II, and 14, I**).
5. Asaas remains responsible for KYC/risk classification, fraud and PLD/FTP controls, and customer service for
   the BaaS services; it may assign accessory tasks to CTech without transferring that responsibility
   (**arts. 9º, 10, 11 and 16**). The implementation and the privacy notice must reflect the operational roles
   agreed in the BaaS contract.

### 0.1 Confirmed Asaas premises (2026-07-30)

These are no longer open design assumptions:

1. **CTech's CNPJ account has cadastral approval and API access.** This satisfies the provider-side onboarding
   premise for the design, without waiving per-subaccount onboarding, transaction monitoring or future review.
2. **The CNPJ restriction applies to the account that creates subaccounts, not to the subaccount holder.** The
   `POST /v3/accounts` reference says that PF accounts cannot *create* subaccounts, while the same endpoint
   defines `cpfCnpj` as the CPF or CNPJ of the subaccount owner and `birthDate` for a natural person. Therefore
   the approved topology is CTech's CNPJ root creating one Asaas payment subaccount in each user's name and CPF.
3. **Published Asaas Terms recognize user ownership and CTech's authority model.** Clauses 5.1.1–5.1.3 describe
   subaccounts opened in CTech clients'/partners' own names. Clauses 5.1.6–5.1.7 require the holders' express
   approval and a specific mandate for CTech to open, move and close those accounts. The versioned mandate and
   acceptance gates in §9 implement that requirement.
4. **The user has no direct Asaas panel, login or API-key access in the contracted BaaS journey.** The CTech wallet
   is the sole technical interface exposed to the user. All subaccount credentials remain server-side secrets,
   and every movement must pass through the wallet ledger, locks, idempotency and authorization gates.
5. **The published Asaas Terms do not expressly prohibit poker.** The CTech account is approved and the user has
   confirmed that its Asaas contract does not prevent the described poker activity. This clears the provider
   contract question for this design; it does not decide whether the game itself is lawful under Decreto-Lei
   3.688/1941 or Lei 14.790/2023, which remains the separate blocker in §0.2.
6. **Conta Escrow is not part of the design.** `HoldGame` is an internal, auditable reservation against the
   user-held balance. It does not transfer ownership, move money to CTech or depend on Asaas's Conta Escrow.

Provider references: [Asaas create-subaccount API](https://docs.asaas.com/reference/criar-subconta),
[Asaas subaccount guide](https://docs.asaas.com/docs/duvidas-frequentes-subcontas) and
[Asaas Terms and Conditions, version updated 27 May 2026](https://central.ajuda.asaas.com/hc/pt-br/articles/32096847160859-Termos-e-Condi%C3%A7%C3%B5es-de-Uso).

### 0.2 Conduct that must not ship

| Prohibited or blocked conduct | Controlling provision | Required correction |
|---|---|---|
| CTech charging in its own name for an Asaas account, Pix receipt, Pix transfer or withdrawal | Res. Conj. 16/2025, **art. 8º, XI**, and art. 15 | Disable the 2% charge and fee sweep until written approval; if it is a payment tariff, Asaas charges and discloses it. |
| User money entering CTech's parent account as part of the BaaS financial service | Res. Conj. 16/2025, **art. 8º, XIV**; Lei 12.865/2013, art. 12 | Keep user balances in client-held payment accounts. Company revenue may enter CTech's account only as payment for a separately valid CTech obligation, with an auditable legal basis. |
| Describing CTech as the provider, custodian, bank or payment institution | Res. Conj. 16/2025, **arts. 8º, §2º, and 14, I** | Identify Asaas and CTech's unregulated platform role in every relevant UI and contract. |
| Operating real-money poker or dominó merely because the platform calls them “skill games” | Decreto-Lei 3.688/1941, **art. 50, caput and §3º, a** | Keep real-money games off until counsel issues a game-by-game opinion based on rules, randomness and expert evidence. |
| Offering an unauthorized product if it is classified as fixed-odds betting | Lei 14.790/2023, **arts. 4º, 6º and 21**; art. 21-A as amended in 2026 | Do not offer it; obtain SPA authorization or redesign only after a formal classification opinion. |
| Treating the game result as an automatically enforceable debt without analyzing the civil-law gaming rule | Código Civil, **art. 814, caput and §§1º–3º**, and art. 815 | Do not execute `HoldGame` settlement in production until counsel confirms whether the exact product is a legally permitted game/competition, whether payment is voluntary, and whether the mandate may execute it. A contract cannot merely disguise, novate or guarantee a non-enforceable gaming debt. |
| Contractually eliminating the seven-day online cancellation right | CDC, **arts. 49 and 51, II** | Do not publish a blanket “no refund” clause. Keep paid sandbox sales disabled until counsel approves a refund/reversal design compatible with the financial invariants. |
| Excluding CTech's consumer liability or transferring it wholesale to Asaas | CDC, **arts. 14 and 51, I and III** | Use a factual dependency notice, without waiver or exclusion of statutory liability. |
| Binding users to terms they could not read, or hiding restrictive clauses | CDC, **arts. 46 and 54, §§3º–4º** | Present the full provider terms and highlighted restrictions before a versioned, evidenced acceptance. |
| Treating biometric KYC data only under ordinary contract performance | LGPD, **arts. 5º, II, and 11** | Use a valid sensitive-data basis, normally art. 11, II, `a` or `g`, as applicable; minimize access and document the controller for each operation. |
| Claiming every ledger/audit record may be kept forever because it is append-only | LGPD, **arts. 15, 16 and 18** | Adopt a record-by-record retention schedule and legal basis. Append-only architecture is not itself a legal basis for indefinite retention. |
| Assuming CTech has no direct PLD/COAF duty | Lei 9.613/1998, **arts. 9º, 10 and 11** | Obtain counsel's classification. If CTech falls within art. 9º (including caput I or parágrafo único VI), register and report as required in addition to cooperating with Asaas. |
| Treating `payment.customer` as proof of the CPF that actually sent a Pix | Lei 9.613/1998, **arts. 10–11**; Res. Conj. 16/2025, **arts. 10–11** | Do not auto-credit on that inference. Asaas documents `customer` as the customer record linked to the charge, not authenticated payer identity. Require an authoritative payer field/transaction query tied to the Pix end-to-end ID, or hold for manual reconciliation. |

Official sources used in this review: [Lei 12.865/2013](https://www.planalto.gov.br/ccivil_03/_ato2011-2014/2013/lei/l12865.htm),
[Resolução Conjunta 16/2025 (BCB)](https://www.bcb.gov.br/estabilidadefinanceira/exibenormativo?numero=16&tipo=Resolu%C3%A7%C3%A3o%20Conjunta) and
[official BCB summary](https://www.bcb.gov.br/detalhenoticia/20950/nota),
[CDC](https://www.planalto.gov.br/ccivil_03/leis/l8078compilado.htm),
[Código Civil](https://www.planalto.gov.br/ccivil_03/leis/2002/l10406compilada.htm),
[Código Penal](https://www.planalto.gov.br/ccivil_03/decreto-lei/del2848compilado.htm),
[Lei das Contravenções Penais](https://www.planalto.gov.br/ccivil_03/decreto-lei/del3688.htm),
[Lei 14.790/2023](https://www.planalto.gov.br/ccivil_03/_ato2023-2026/2023/lei/l14790.htm),
[Lei 9.613/1998](https://www.planalto.gov.br/ccivil_03/leis/l9613compilado.htm), and
[LGPD](https://www.planalto.gov.br/ccivil_03/_ato2015-2018/2018/lei/l13709compilado.htm).

---

## 1. Problem — the current model is a custody defect, not a bug

Every real today lands in **one** Banco Inter PJ account owned by A O CARVALHO TECH (CNPJ 62.787.449/0001-07).
The `wallets` table is a private claim ledger against that single pooled balance. Consequences:

| Consequence                                     | Why it is unacceptable                                                                                                                                                              |
|-------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| **Commingling (conta omnibus)**                 | Third-party money is indistinguishable from company money in the same account.                                                                                                      |
| **Bankruptcy / execution pooling**              | If the Inter balance is an ordinary CTech account rather than a legally segregated payment account, user claims are exposed to CTech's creditors. The legal conclusion depends on the Inter account contract. By contrast, resources actually held in payment accounts receive the segregation of Lei 12.865/2013, art. 12. |
| **Apropriação indébita exposure (CP art. 168)** | Possession of third-party funds creates material criminal risk if CTech later acts as owner. The offence is not established by an accidental bookkeeping transfer alone: art. 168 criminalizes **appropriating** movable property held or possessed for another. Intent and the facts matter. |
| **Unauthorized payment-service exposure**       | Lei 12.865/2013, art. 6º, III, includes providing cash-in/out, executing or facilitating payment instructions and managing payment accounts. Whether CTech is only a BaaS tomadora or itself performs regulated activity depends on the executed model and Res. Conj. 16/2025; it cannot be declared solved by architecture alone. |
| **The terms addendum admits it**                | `docs/legal/wallet-terms-addendum.md` §6 literally says *"atua como intermediário técnico de custódia"* — a claim CTech is not licensed to make.                                    |

The Asaas BaaS + per-user-account model can correct the custody layer **only if** the executed arrangement meets
Res. Conj. 16/2025: the account is legally held by the client at Asaas (art. 4º, §2º), the client contracts
directly with Asaas for those services (art. 3º, IV), and CTech remains within the permitted tomadora role. It
does **not** by itself settle CTech's regulatory perimeter, the games classification, PLD/FTP, consumer or mandate
questions — see §§0, 9 and 14.

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

## 3. Target model — account held by the user at Asaas

```
                    CTech parent Asaas account (CNPJ) — company money ONLY (rake, subscriptions)
                     │  creates subaccounts, holds parent API key, receives ACCOUNT_STATUS_* webhooks
                     │
   ┌─────────────────┼─────────────────┬─────────────────┐
   ▼                 ▼                 ▼                 ▼
 sub(user A)      sub(user B)      sub(user C)  ...   (one Asaas subaccount per user, in the USER'S name+CPF)
 EVP pix key      EVP pix key      EVP pix key         R$13,90 one-off each
 balance = A's    balance = B's    balance = C's       ← Asaas clauses 5.1.1–5.1.3: account in the user's name
```

**The new load-bearing invariant (#13):**
> At quiescence, `Σ Asaas subaccount balances == Σ(real + game + open holds)`. At all other times, both the
> system-wide and per-user differences must be exactly explained by durable, idempotent
> `settlement_pending_in/out`, withdrawal and provider-fee-reserve legs. Pending inbound winnings are
> not withdrawable, returnable to `real`, or usable in another game until the corresponding Asaas transfer is
> `DONE`. `sandbox` is excluded because it is virtual; unexplained drift must alarm and fail closed.

CTech's own account must never hold a centavo of user money. The API schema and Asaas Terms identified in §0.1
support the required user-held topology under Res. Conj. 16/2025, art. 4º, §2º. Resources held in the user's
payment account receive the protection of Lei 12.865/2013, art. 12. This addresses commingling and insolvency
segregation, but CTech must still remain within the holder's express authority when controlling instructions.

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

The QR is generated on the user's approved Asaas subaccount. The endpoint identifies `cpfCnpj` as the document of
the subaccount owner, and Asaas Terms clauses 5.1.1–5.1.3 describe the account as opened in that client's or
partner's own name. This implements Res. Conj. 16/2025, art. 4º, §2º.

|                                         | QR on CTech's account                                            | QR on the user's subaccount ✅                            |
|-----------------------------------------|------------------------------------------------------------------|----------------------------------------------------------|
| Custody during deposit                  | **CTech holds third-party money**, then transfers                | money is the user's from the instant it settles          |
| The defect this migration exists to fix | **reintroduced** on every single deposit                         | absent                                                   |
| Extra failure mode                      | internal transfer can fail → money in limbo in CTech's account   | none — no transfer step exists                           |
| Static-QR count / rate limits           | all deposits on one account → the ceiling the user worried about | spread across N accounts; the ceiling concern disappears |
| Asaas free-Pix quota                    | one account's quota for all users                                | one quota **per subaccount** (confirmed — §10 Q1)        |
| Payer-facing name on the QR             | "A O CARVALHO TECH"                                              | the user's own name                                      |

The only real argument for the CTech account is the QR showing the company name. That is cosmetic, and it cuts
the other way: a QR in the user's own name is *evidence* the money is theirs, which is exactly the story this
migration tells. Handle it with UI copy, not architecture:

> "Sua conta de pagamentos está no seu nome (CPF ***.***.***-**), aberta para você no Asaas (instituição
> autorizada pelo Banco Central). Ao depositar você está transferindo para a sua própria conta — a CTech nunca
> retém o seu dinheiro."

Paying yourself is already a familiar act (Nubank → Mercado Pago). It reads as safe, not as broken.

**Prohibited for this BaaS flow:** deposit into CTech's account + internal transfer. Res. Conj. 16/2025,
art. 8º, XIV, requires the BaaS contract to prohibit the tomadora from receiving in its own account values
related to the financial services supplied to clients.

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
and `ctech-account`'s KYC is the identity service. If Asaas confirms the field is necessary, collect it in the
**wallet's own real-wallet activation form** and pass it through with the transparency required by LGPD arts. 6º,
9º and 18. Collection, use and transmission are processing even if CTech does not persist the value.

Better still: **do not persist it** unless a documented legal or contractual duty requires retention. Income is
personal data (although not “sensitive personal data” in the exhaustive LGPD art. 5º, II definition). Send it and
drop it. Do not append even a coarse income bucket to `wallet_audit` without a defined purpose, necessity test and
retention period (LGPD art. 6º, I–III).

So the `ctech-account` change reduces to one thing: extend the internal KYC read — `kycclient.KYC` currently
returns only `Level, CPF, LegalName, BirthDate` — to also return **email, phone and address**. `Address.City` /
`.State` come along as the fallback Asaas requires when a CEP does not resolve. No new KYC field, no new form
there, no migration.

#### 5.1.2 Subaccount API-key storage — do NOT use Secrets Manager

One key per user, returned once, and the parent can only rotate it inside a **2-hour window manually enabled in
the Asaas web UI** (BaaS clients excepted — confirm, §10 Q5). Losing a key is therefore expensive.

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
         ├─ payer CPF: authoritative Pix-transaction source tied to endToEndId          (§5.2.2)
         │             (`payment.customer` is NOT proof; launch blocked until §10 Q15)
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

**Correction after the second legal review:** `GET /v3/customers/{payment.customer}` cannot establish the actual
Pix payer. Asaas documents `customer` as the customer record to which a charge is linked; it may be populated by
the application before anyone pays. Comparing that record with the wallet user's CPF merely compares the user
with data the platform already supplied and does not detect a third-party Pix.

The same-CPF deposit rule therefore remains a launch blocker until Asaas identifies an authoritative API or
webhook field for the actual debit-account holder, tied to the Pix `endToEndIdentifier`. The webhook may wake the
flow, but the credit decision must re-query that authoritative source. If no such source is available, do not
silently weaken the rule: leave the payment in `payer_verification_pending`, block use of the amount and route it
to a documented manual/refund process. This is both an Invariant #11 issue and a PLD/FTP control.

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
   credit nothing automatically, and put it through the authoritative payer-verification process above as an
   unsolicited inbound payment.

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
         ├─ ctech_fee = 0                                   [legal hold; §6.4]
         ├─ reserve provider_fee_max                        [Asaas tariff, not CTech revenue; see below]
         ├─ DebitWithProviderFeeReserve(amount, provider_fee_max)
         └─ POST /v3/transfers (SUBACCOUNT key)
                { value, pixAddressKey: kyc.CPF, pixAddressKeyType: "CPF",
                  externalReference: withdrawalID, operationType: "PIX" }
              ├─ Asaas calls our TRANSFER-VALIDATION webhook (§5.3.2) → we APPROVE/REFUSE
              ├─ status PENDING/BANK_PROCESSING → stays `processing`   [Invariant 12]
              ├─ response exposes transferFee = T
              ├─ append provider-fee adjustment +(provider_fee_max − T); never rewrite an entry
              ├─ status DONE  → completed; no CTech fee sweep while §6.4 legal hold is active
              └─ FAILED/CANCELLED → reverse amount + remaining provider-fee reserve
3. cmd/reconcile: GET /v3/transfers?externalReference=<withdrawalID> → complete or reverse
```

`ctech_fee` and `provider_fee` are different legal and accounting objects. While §6.4 is blocked, CTech charges
zero. Asaas may still debit its own tariff directly from the user-held subaccount. The wallet must show that
provider tariff before confirmation and mirror the exact debit in a distinct `EntryProviderFee`; otherwise the
Asaas balance becomes smaller than the ledger by `T`.

If Asaas does not expose an authoritative pre-transfer quote, reserve a named, contractually hard maximum
`AsaasPixTransferFeeMax` before the call and release the unused portion with a compensating append-only credit
after `transferFee` is known. Never guess from a free-quota counter alone. If the provider can charge more than
the configured maximum, disable withdrawals until the contract/API supplies a safe bound. A normal full-balance
withdrawal pays `balance − T`; only the account-closure flow in §5.6 is CTech-funded so that 100% of the user's
balance can be returned.

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

That fact changes the technical problem. When A loses R$100 to B, the money is still physically in **A's**
subaccount while the ledger records B's claim. Within the contracted BaaS interface, A cannot technically move
the held amount. The technical exits are:

| Exit                          | Gate                                                           |
|-------------------------------|----------------------------------------------------------------|
| Asaas panel / direct Pix by A | **does not exist** — no access                                 |
| `POST /wallet/withdrawals`    | our API: open hold + `ConditionExpression: balance >= :amount` |
| Direct technical channel      | none is exposed in the confirmed BaaS journey                   |

This avoids platform-wide commingling, but it does **not** eliminate execution risk. Until the Asaas transfer is
complete, a winner's ledger claim is still physically backed by a loser's subaccount and may be hit by a judicial
or compliance block. The risk is bounded by the drift window and must never be allowed to propagate through a
second game or withdrawal.

#### 5.4.1 Rejected approaches

- ❌ **Transfer per hand/round.** Chips move continuously and a hand in progress has money in a pot that belongs
  to nobody yet. Settling mid-hand settles a fiction. Also: hundreds of API calls for a state that is about to
  change again.
- ❌ **Asaas Conta Escrow.** It is not required and must not be enabled for this flow. It retains qualifying
  subaccount receipts under a provider-configured release mechanism; it is not the per-buy-in reservation used
  by the game ledger. Enabling it would add a second blocking state and reconciliation surface without replacing
  `HoldGame`.
- ❌ **Pot/omnibus account per table.** Buy-ins into a CTech-owned "mesa" account reintroduces commingling for
  the single most scrutinized category of money in the system. Rejected on the same grounds as the whole
  migration.
- ❌ **Never converge.** Attribution exposure grows without bound and Invariant #13 becomes decorative.

#### 5.4.2 The model: hold-anchored custody, converged at safe points

**The hold is the custody anchor.** At buy-in, `HoldGame` debits `game` and the money stays where it is,
immobilized. Nothing physical happens. This is already the current behaviour — no change.

Here, “immobilized” means **unavailable through the CTech wallet ledger**, not an Asaas Conta Escrow or a
provider-side judicial block. This is operationally enforceable because the contracted BaaS journey gives the
holder no direct panel, login or API credentials: the wallet is the sole technical movement channel. Every debit, withdrawal and
settlement must use ledger `available` balance and must never authorize against the larger raw Asaas balance.
The user's title to the full underlying subaccount balance is unchanged throughout the hold.

**Settlement is a recorded obligation before it is a transfer.** When the poker engine settles (`CashoutGame`,
table close), the ledger entries are written first, in the same `TransactWriteItems` as always. The internal
transfer that follows is the *execution* of an obligation that is already durable. A failed transfer therefore
never loses the record — it retries. The ledger is durable evidence of what must be executed, but it is **not a
guarantee of payment** if the source account is frozen or the underlying obligation is legally unenforceable.

**A recorded obligation is not yet spendable custody.** Any net gain credited by `CashoutGame` is simultaneously
encumbered as `settlement_pending_in`. It is shown separately in the UI and is excluded from every available-
balance check: withdrawal, `game → real`, a new `HoldGame`, sandbox purchase and account closure. The matching
loser-side amount is `settlement_pending_out` until the provider transfer is final. Only a `DONE` Asaas transfer
atomically clears both pending markers; a failed or blocked leg remains unavailable and escalates to
reconciliation. This prevents an unfunded win from being replayed through multiple tables.

**Converge at three points, in this priority:**

1. **Table close (`settlement batch`).** All stacks are final, no hand in progress, amounts are settled. Compute
   the **net delta per player** for the session and emit a *netted* set of internal transfers: net losers → net
   winners, greedy-matched, at most `N-1` legs for `N` players instead of `N²`. Whether multilateral netting may
   match players without a direct bilateral debt is a legal question (§14). The platform's **fixed table access
   fee** — not a rake; real-money tables take no share of the pot (§9.4, Invariant #15) — may be a separate leg
   only if counsel confirms that it is valid consideration for a non-financial CTech service and may enter the
   parent account under Res. Conj. 16/2025, arts. 8º, XIV, and 15.
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

- A `settlement` row per batch: `batch_id` (ULID), table ref, the computed legs, status per leg and the matching
  `settlement_pending_in/out` encumbrances.
- Each leg carries `externalReference = <batch_id>#<leg_n>`.
- Before sending any leg, `GET /v3/transfers?externalReference=<batch_id>#<leg_n>` — adopt an existing transfer,
  never re-send.
- A leg that fails or is ambiguous stays `pending`; the sweep retries it. Partial batches are expected, but the
  related winnings remain unavailable. They are safe only while every unresolved amount is exactly explained by
  an encumbrance and cannot be spent again.

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

Pulling money from A's account to B uses the express authority required by Asaas Terms clauses 5.1.6–5.1.7 and
implemented by the separate, versioned mandate in §9.2. The provider account/API approval is confirmed (§0.1);
the remaining launch conditions include counsel's classification of the game, the effect of Código Civil art.
814 on automatic settlement, and validation of the mandate wording.

### 5.5 New failure mode: a frozen or blocked subaccount

With an omnibus account, a judicial block was catastrophic but singular. Now a block can hit **one user's**
subaccount — and Asaas emits balance-block webhook events for exactly this. The ledger must model it:
a `frozen` state on the wallet, withdrawals and settlements refused with a distinct problem type
(`account-blocked`), and an operator surface. Silently failing a withdrawal because the destination account is
frozen is precisely the "money in limbo" Invariant #12 forbids.

### 5.6 Revocation of authority / account closure — **does not exist and must be built**

The user must be able to end both relationships and recover the balance. Res. Conj. 16/2025, art. 8º, §6º, II,
requires the BaaS contract to address continuity and allow the client to choose closure of the relevant
relationship; any mandate also ends by revocation under CC art. 682, I. That makes this flow a **launch blocker**,
not a nice-to-have. Verified against `api/internal/api/v1/router.go`:
there is **no closure or revocation route today** — the closest things are `gambling/self-exclude` (blocks play,
keeps the account) and `gambling/limits`. Nothing returns the balance and nothing tears down the account.

New route: `POST /v1.0/wallet/closure` — user JWT, `RequireRecentMFA` (it moves the entire balance out), and an
`Idempotency-Key`. Steps, in order:

1. **Refuse if not settleable.** Open holds, `settlement_pending_in/out`, a hand in progress, or a `processing`
   withdrawal → `409`. Revocation
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
6. **Disable the wallet** for that user: `closed`, `authority_revoked_at`, and a `wallet_audit` row describing
   closure of the Asaas and CTech relationships and, if one exists, the mandate version revoked.

**Do not make it a single request.** Each step is externally-visible and independently retryable; model it as a
state machine on the wallet row (`closing → paid_out → subaccount_closed → closed`) driven by the same
reconciliation job as §5.3, so a failure between the payout and the Asaas closure resumes rather than stranding
the user half-closed. A closure stuck with money already sent but the account still open is Invariant #12
territory.

**Reopening** is a new account and any new commercial opening cost (§6.5), a fresh direct Asaas contract and a
fresh acceptance of the mandate required by Asaas Terms clauses 5.1.6–5.1.7. State verified consequences in the
UI before confirmation.

Extinction of the mandate is not only by revocation: **CC art. 682, II — the mandate ends on the death or
interdição of either party.** At that point CTech cannot rely on the prior mandate to move the balance; counsel
must define the limited preservation and succession procedure (§9.8). Support
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

1. **Detect it.** Subscribe to the Asaas event for it (§10 Q11: which webhook event, and what the
   notification window is). Do not discover MED from a balance mismatch.
2. **Debit what is there**, conditionally, exactly as today; the shortfall becomes an explicit
   `med_receivable` row against the user — a *debt*, not a negative balance. Invariant #1 stays literally true:
   `wallets.balance` never goes below zero.
3. **Block withdrawals and funding while a `med_receivable` is open**, and settle it from the next inflow. This is
   the only place in the system where the wallet holds a claim against the user, so it must be a distinct concept
   with its own problem type, not a negative number smuggled into a balance.
4. **A `med_receivable` must never be netted into the conservation check** of §5.4.4 without being named, or the
   check silently stops meaning anything.

Cheapest mitigation, and it is worth more than the machinery above: enforce the intended same-CPF deposit rule
using actual payer data (§5.2.2). It is **not yet enforceable with `payment.customer`**. Once the authoritative
source in §10 Q15 is available, same-CPF-only deposits become a strong structural filter, although not a complete
one — a user whose own account was taken over is still a MED case — so the receivable path still has to exist.

**⚠ Related hole in the same section: refunding a rejected deposit costs money that nobody has agreed to pay.**
When a third-party deposit is auto-refunded, Asaas has *already* debited the receiving tariff from the user's
subaccount. Refunding the full amount drives that subaccount negative by the tariff. Counsel's draft handles it by
deducting costs from the refund ("deduzidos eventuais custos operacionais") — but the payer is a stranger who
never contracted with CTech, and returning less than was sent to someone with no contract is a fight not worth
having over R$1,99. **Refund in full and cover the tariff with a parent → subaccount transfer**, booked as an
operating cost. That is a transfer leg that does not exist in this spec yet, and it belongs in `refundExcessPayments`
(`api/internal/services/wallet.go:416`).

---

## 6. The fee question — **not legally cleared**

> *"A principal questão é a taxa de processamento, deveria cobrar a taxa de processamento do usuário diretamente logo?"*

**Asaas may charge a tariff permitted by the payment regulation; CTech may not charge in its own name for the
financial or payment product supplied by Asaas.** Res. Conj. 16/2025, art. 8º, XI, requires that prohibition in
the BaaS contract, and art. 15 limits BaaS tariffs to those permitted to the authorized institution. Any Asaas
free quota is a commercial fact to be confirmed contractually and must not be hard-coded as a legal entitlement.

> **Confirmed:** each subaccount has its own quota — roughly 100 free received Pix and ~30 free transfers per
> month, *per user*. A normal user will never exhaust it. That collapses the fee from "a permanent per-transaction
> cost" to "an edge case for heavy users", and it is the single most consequential answer in §10.

### 6.1 Why it is already the user's

Once the QR lives on the user's subaccount, Asaas debits R$1,99 from **that** account. `netValue < value`.
Crediting `netValue` (§5.2.1) means the user bore the cost. There is no decision to make about *who pays* — only
about whether CTech **reimburses** it. Recommendation: no.

### 6.2 On deposits, do not invoice the tariff as a CTech charge

Deposits carry **no CTech fee**. The proposed withdrawal charge is also disabled pending §6.4 clearance. For the
deposit path the framing is simply:

> The tariff is **Asaas's**, debited by Asaas from the user's own account, disclosed by us.

Charging separately *for receiving a Pix* in CTech's name is prohibited in this BaaS model by Res. Conj.
16/2025, art. 8º, XI. Disclose any Asaas tariff and identify Asaas as its creditor; do not invoice it as CTech.

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

### 6.4 Proposed withdrawal charge — **disabled pending legal and Asaas approval**

The proposed formula is `clamp(amount*200/10000, 250, 1000)`, but it is **not approved to ship**. Because the
charge arises only when the user requests the Asaas payment service and expressly covers the transfer tariff,
there is a substantial risk that it is remuneration for Asaas's product, which CTech cannot collect in its own
name under Res. Conj. 16/2025, art. 8º, XI. Contract wording cannot cure a mismatch between label and substance.

If and only if Asaas and counsel approve it as consideration for a genuinely separate CTech service, the
consumer must see the total price and basis before contracting (CDC arts. 6º, III, 31 and 46), and the amount must
not create manifestly excessive advantage (CDC art. 51, IV and §1º). Everything else is
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

**Do not rely on naming.** Calling it `tarifa de serviço da carteira` does not make a withdrawal-triggered charge
lawful. Until written clearance, the implementation value is zero, no fee ledger entry is created, and no fee
sweep is sent to the parent account. The remaining calculations in §§6.4.1–6.4.3 describe a contingent technical
option only and do not authorize production use.

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

Leg 2 is the same technical mechanism as a separately approved platform-fee transfer (§5.4.2). It may be used
only after §§6 and 14 establish that the underlying CTech charge is lawful; the transfer mechanism itself does
not make the revenue legitimate.

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

This calculation is technical only. A free Asaas quota does not become CTech margin while the CTech charge is
disabled under §6.4 and Res. Conj. 16/2025, art. 8º, XI.

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
| 9      | `game` is real money                                    | **externally backed in the confirmed topology** — each subaccount is opened in that user's name/CPF under Asaas Terms clauses 5.1.1–5.1.3 and reconciled to the ledger                                                                                                  |
| 10     | Consent opt-in and auditable                            | **extended** — direct Asaas terms/disclosures and the express mandate required by Asaas Terms clauses 5.1.6–5.1.7 are separately versioned; subaccount creation and key rotation become `wallet_audit` events                                                            |
| 11     | Webhook never the source of truth                       | **preserved with a blocker** — re-query `GET /v3/payments/{id}` and require `RECEIVED`; `payment.customer` is not payer proof. No automatic credit until an authoritative payer-identity query tied to the Pix transaction is defined (§5.2.2)                                  |
| 12     | No money in limbo                                       | **preserved and widened** — new limbo states: subaccount `onboarding`/rejected, frozen subaccount (§5.5), custody drift (§5.4)                                                                                                                                          |
| **13** | **NEW: ledger equals custody**                          | system totals must match; per-user drift must be zero or exactly explained by durable settlement, withdrawal or provider-fee-reserve legs. Pending winnings are non-spendable until Asaas reports `DONE`; unexplained drift blocks withdrawals and new games                                      |
| **14** | **NEW: CTech's own account never holds user money**     | every path that would place user funds in the parent account is a bug, whatever it enables                                                                                                                                                                              |
| **15** | **NEW: CTech is never a counterparty in a game, nor takes a share of a real-money pot** | mandatory risk control, **not proof of legality**. Real-money games remain disabled until the game-by-game opinion in §9.4; a house position or percentage of a pot remains forbidden by product policy                                                                   |

---

## 8. Cost model

| Item                                            | Cost                                      | Who bears it                      | Note                                                           |
|-------------------------------------------------|-------------------------------------------|-----------------------------------|----------------------------------------------------------------|
| Subaccount creation                             | R$13,90 one-off                           | CTech                             | lazy creation only (§6.5)                                      |
| Pix received (static QR)                        | R$1,99, ~100 free/month                   | user, via `netValue`              | free-quota scope is **Q1** below                               |
| Pix withdrawal (Asaas tariff)                   | commercial value; confirm contract         | as defined by Asaas/user contract | CTech charge disabled; do not assume quota or margin           |
| Proposed CTech withdrawal charge                | **disabled**                              | —                                 | blocked by §0 and §6.4 pending art. 8º, XI clearance           |
| Internal transfer (settlement, entry fees, fee sweep) | **R$0**                                   | —                                 | makes §5.4 and §6.4.2 viable                                   |
| EVP Pix key                                     | R$0                                       | —                                 | 1/min per account rate limit                                   |

---

## 9. Legal implications

Not legal advice. This section is engineering's map of the exposure, for counsel to rule on. The gambling and
tax items need a lawyer and an accountant **before** launch, not after.

### 9.1 What the confirmed BaaS custody structure fixes — and what it does not

- **Commingling** — the API fields and Asaas Terms clauses 5.1.1–5.1.3 support accounts in each user's name, so
  the model separates user and CTech money.
- **Bankruptcy / execution pooling** — resources maintained in a payment account are separate from the payment
  institution's estate by Lei 12.865/2013, art. 12. The executed account and BaaS contracts must prove that this
  protection applies to each account.
- **Apropriação indébita risk** — the model reduces CTech's factual possession, but does not legalize an
  instruction outside its authority. CP art. 168 requires appropriation; neither risk nor innocence follows from
  the database topology alone.
- **Payment-regulation perimeter** — Res. Conj. 16/2025 expressly recognizes a tomadora role, but confines it.
  Asaas's authorization does not license CTech to perform regulated acts on its own account.

This is a material improvement under the confirmed account/API premises. It does not validate the legal nature
or civil enforceability of the game, the mandate language, fees, PLD/FTP allocation or tax treatment.

### 9.2 What it does NOT fix — authority to instruct the account

**Control is not ownership.** CTech holds credentials capable of initiating movements from client-held accounts.
Res. Conj. 16/2025 now requires the user to contract directly with Asaas for the financial/payment services
(art. 3º, IV) and makes Asaas responsible for those services (arts. 9º, 10 and 16). The source of CTech's
instruction authority must therefore be the executed Asaas-user and Asaas-CTech contracts plus the holder's
express authorization. Asaas Terms clauses 5.1.6–5.1.7 expressly require approval and a specific mandate when
CTech opens, moves and closes accounts as mandatary. That mandate cannot replace the user's contract with Asaas,
make a debt enforceable contrary to Código Civil art. 814, or expand CTech into regulated activity.

The required **contrato de mandato** is governed by CC arts. 653–692. It must be express, versioned and audited,
and must comply with the private-instrument content requirements of art. 654, §1º (place, qualification of both
parties, date, object and extent of powers), the form required for the underlying act (art. 657), special and
express powers for acts beyond ordinary administration (art. 661, §1º), diligence (art. 667) and accounting
(art. 668). It must grant and bound only acts the regulated BaaS arrangement permits:

1. Opening a payment account at Asaas in the user's name and on their behalf.
2. Requesting a Pix key (EVP) and generating collection QR codes on that account.
3. Executing Pix transfers **only** to a key belonging to the user's own verified CPF (withdrawal), and internal
   transfers **only** to settle legally valid obligations recorded in the CTech ledger (player settlement and,
   if separately approved, a fixed platform access fee).
4. Explicit prohibitions: no third-party destinations, no use of balance for CTech's own account, no credit
   operations.
5. Revocability, and what revocation means operationally (balance returned to the user's own Pix key, account
   closed).

Acceptance must be versioned exactly like the existing terms/gambling addenda
(`CurrentTermsAddendumVersion` pattern — computed equality, never a stored boolean) and appended to
`wallet_audit`. Bumping the mandate version re-gates money movement, and per the existing rule
(three-wallet spec) **must never trap the user's money** — returning funds to the user must always remain
available.

**Do not assume a checkbox inside general terms is sufficient.** Although CC art. 656 permits an express or tacit,
written or verbal mandate in general, arts. 654, §1º, 657 and 661 impose content, form and special-power rules for
this use. The electronic evidence must identify the signed version and parties and be accepted by Asaas for the
acts it will execute. Use a separate highlighted electronic instrument unless counsel expressly approves another
form. At minimum:

1. **Specificity.** CC art. 661: a mandate *in general terms* confers only powers of ordinary administration;
   anything beyond that requires express, special powers. A clause saying "podemos administrar sua conta" grants
   nothing useful. It must enumerate the acts of §9.2 items 1–4 individually — open the account, request the Pix
   key, generate QR codes, transfer **only** to the holder's own verified CPF, transfer internally **only** to
   settle obligations recorded in the ledger — and state the prohibitions explicitly.
2. **Prominence.** CDC art. 54 §4: clauses that limit consumer rights must be highlighted. A separate,
   distinctly-presented acceptance step — not a line buried in the general terms — and CDC art. 51 makes a blanket
   authorization voidable as abusive. Specificity plus revocability is what keeps it out of that bucket.
3. **Revocability, accounting and a working exit.** CC art. 682, I, ends the mandate by revocation; arts. 667 and
   668 require diligence and rendition of accounts. Revocation must have a real
   operational meaning — balance returned to the user's own Pix key, subaccount closed — and that path must
   already be built, not promised (§9.8).

Wording is counsel's call. The engineering requirement is that the *acceptance* is a versioned, audited gate on
money movement, exactly like `GamblingAccepted()` is today.

**The provider contract also allocates material liability to CTech.** Asaas Terms clause 5.1.4 states that the
root-account holder assumes solidary responsibility for obligations of linked subaccounts, including
chargebacks, cancellations, refunds, information and movements; clause 5.1.5 makes the parent account a guarantor
for specified negative subaccount events in White Label arrangements. This does not authorize CTech to debit
another user's balance or create a negative wallet. Counsel must reconcile those clauses with CDC rules, the
loss allocation for MED/chargebacks and Invariants #1, #13 and #14. Any provider claim is paid from CTech's own
funds unless a separate, valid and contestable claim against the affected user has been established.

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

The historical draft is in **§13**, but is withdrawn and must not be applied to `ctech-account`
(`ui/src/lib/legal-documents.ts`). The list below records the earlier review only.

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

Identification is a regulatory obligation, not merely an Asaas documentation preference. Res. Conj. 16/2025,
art. 14, I, requires accessible, visible identification of the BaaS institution in channels, interfaces,
contracts, documents and payment instruments; art. 8º, §2º, I–II, requires the parties to tell the client that
CTech does not act in Asaas's name and is not a BCB-authorized institution for these services. The addendum and UI
must state this accurately. Replace `docs/legal/wallet-terms-addendum.md` §6's “intermediário técnico de
custódia” claim; CTech is the BaaS tomadora/platform and Asaas supplies the regulated account/payment service.

### 9.4 Games — classification is unresolved and real-money operation is blocked

The draft's categorical statement that poker and dominó “are games of skill” is not a safe legal conclusion.
Decreto-Lei 3.688/1941, art. 50, caput, prohibits establishing or exploiting a game of chance in a place open or
accessible to the public, and §3º, `a`, defines the prohibited category by whether gain and loss depend
exclusively or **mainly** on chance. Whether skill predominates is a factual question about each exact rule set,
randomization mechanism, time horizon, matchmaking and prize structure. A product name, absence of a house
position, fixed entry fee or recognition of poker as a sport does not by itself answer that statutory test.

The SPA states that skill games, multi-player games and P2P games fall outside the regulated category of virtual
fixed-odds online-game events under Portaria SPA/MF 1.207/2024. That exclusion is helpful but narrow: being outside
Lei 14.790/2023 does **not** affirmatively legalize a product under the Contraventions Act. Conversely, if the
product is found to contain a fixed quota or otherwise falls within Lei 14.790/2023, prior federal authorization
is required by arts. 4º and 6º, payment institutions must not process an unauthorized operator under art. 21, and
art. 21-A (2026 wording) allows blocking operator accounts and related transactions.

There is a second, independent civil-law gate. Código Civil art. 814 provides that gaming or betting debts do
not compel payment and extends the rule to contracts that disguise recognition, novation or guarantee of such a
debt. Article 814 §2 applies even to a game that is not prohibited, except legally permitted games and bets; §3
also excepts qualifying sporting, intellectual or artistic competitions that comply with applicable rules. The
fact that a player clicked “aceitar”, that CTech reserved funds internally, or that a mandate authorizes account
movements does not by itself answer which branch applies. If the obligation is not legally enforceable, the
mandate cannot be used as a drafting device to force its settlement.

Counsel must therefore decide whether the buy-in is already a voluntary payment before play, whether a mere
internal hold can have that effect while title remains with the player, whether automatic post-result transfer
is voluntary payment or enforcement of a gaming debt, and whether poker qualifies for an exception in art. 814,
§2 or §3. Until that answer is written, `HoldGame` may exist for sandbox testing only and no real-money settlement
instruction may be sent to Asaas.

Therefore `GAMBLING_ENABLED` must remain false for real money until counsel provides a signed, game-by-game
opinion supported by the final rules and, where needed, a technical probability/skill report. The opinion must
separately cover poker and every dominó mode; a poker conclusion cannot be reused for dominó.

The following structural facts reduce exposure but **do not decide classification**; keep them as mandatory
controls if counsel approves the product:

1. **CTech is never a counterparty.** Every real-money game is player versus player. The house never takes a
   position, never covers a loss, never wins. There is no `banca` to explore.
2. **No share of a real-money pot.** Real-money tables are rake-free (`ctech-poker` `hand.ConfigureRake` →
   `rakeBPS = 0`); the platform charges a **fixed entry fee per tier** (R$1/2/4/8, `api/internal/api/v1/stakes.go`).
   A fixed price for access to a service is a service price; a percentage of the pot is a share in the game's
   result, and it is the single fact that most invites an `exploração de jogo` reading. It is already absent —
   the requirement is to keep it absent.
3. **No fixed quota.** Lei 14.790/2023, art. 2º, II, defines quota fixa by the multiplier that determines the
   award. P2P status or absence of a house counterparty is not alone conclusive; counsel must confirm that the
   prize mechanics contain no fixed-odds structure.

**Vocabulary must be accurate, not euphemistic.** Art. 50, §3º, `c`, also addresses bets on other sporting
competitions, but replacing `aposta` with `entrada` cannot change the legal nature of a product. User-facing and
contract text must describe the actual flow in plain language (CDC arts. 6º, III, 31 and 46). Counsel may choose
precise terminology after classification; engineering must not conceal staking, loss or prize mechanics.

**What the residual risks actually are:**

- **Continuing provider enforcement.** The account/API approval and poker contract premise are confirmed (§0.1),
  but Asaas Terms clauses 19–20 preserve monitoring, blocking and termination for illegality, irregularity or
  later commercial refusal. The design must retain a provider-block contingency; approval is not a promise that
  no future review will occur.
- **Regulatory evaluation period is a hard launch gate** (§10 Q3): max 10 subaccounts per distinct holder,
  R$2.000 per subaccount, 60 days from the first subaccount, after which *creation is automatically blocked*.
  A public launch that outruns evaluation clearance stops mid-onboarding with users stranded in `onboarding`
  state. **This gate is on the wallet, not on poker** — the caps are on subaccount creation and per-subaccount
  volume, so deferring the poker launch does not defer it. Clear it before the first public wallet signup.

The responsible-gaming machinery is unaffected and should stay exactly as it is: `real → game` is still one
internal edge, still the only door, still metered gross, and Invariant #7 survives the custody change intact.
Personal limits and self-exclusion remain prudent harm controls, but they do not prove that the product is lawful
or outside a licensing regime.

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

Do **not** state that CTech has no formal COAF-registration duty. Res. Conj. 16/2025, arts. 10–11, makes Asaas
responsible for the BaaS PLD/FTP policy while requiring CTech's cooperation. Separately, CTech's own model may
fall within Lei 9.613/1998, art. 9º, caput, I (intermediation of third-party financial resources), or parágrafo
único, VI (other systems for collecting bets and paying prizes), depending on the games classification and flow.
If so, arts. 10 and 11 impose direct registration, records, controls and reporting duties. This is a launch
question for counsel and, if necessary, a formal consultation to COAF/SPA; it cannot be delegated away by the
BaaS contract.

### 9.6 Tax and invoicing

- **Table entry fees** may be CTech revenue, but their legality and tax/service classification depend on the
  game-by-game opinion and municipal/federal tax advice. A `serviço de tecnologia` label is not determinative.
- **Proposed withdrawal charge (2%)** is disabled under §6.4. Do not invoice or sweep it unless Asaas and counsel
  first clear Res. Conj. 16/2025, art. 8º, XI, and an accountant determines the correct tax treatment.
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

Full name, CPF, birth date, address, phone and income are personal data; selfie templates and other biometrics are
sensitive personal data (LGPD art. 5º, II). Ordinary account-opening data may rely on contract performance where
art. 7º, V, actually applies. Biometrics require an art. 11 basis — commonly legal/regulatory obligation under
art. 11, II, `a`, or fraud prevention/authentication under art. 11, II, `g`, as determined by the relevant
controller. Asaas cannot be labelled globally as CTech's “operator”: under Res. Conj. 16/2025 it contracts with
the client and decides regulated KYC/PLD processing, which strongly indicates an independent-controller role for
those operations. The privacy notice and data-processing agreement must map roles purpose by purpose. The
`onboardingUrl` path minimizes CTech's access but does not eliminate CTech's processing of data it collects and
transmits.

A deletion request under art. 18 must be assessed record by record. Legal/regulatory retention may justify
conservation under art. 16, I, but the append-only invariant is an engineering control, **not** a statutory legal
basis for indefinite retention. Before launch, counsel must define the source and period for each ledger, audit,
KYC, webhook, device and security record; after that period, delete or irreversibly anonymize where legally
permitted. Account closure is distinct from erasure, and the privacy notice must explain both without promising
either blanket deletion or blanket perpetual retention.

### 9.8 Succession, closure, and the user's right to leave

The account is in the user's name. On death, succession law applies to the balance — CTech cannot simply move it.
On request, the user must be able to close and take their money out. Both need a documented process; neither
existed under the omnibus model because the question could not even be asked.

### 9.9 Illegality review of the drafted text (2026-07-29)

Second pass over the whole document, looking specifically for clauses and mechanics that are **unlawful, void or
unresolved**. The game classification is not settled (§9.4). These are preventive conclusions; counsel must
confirm them against the final contracts and product.

**(a) The liability exclusion in §13 clause 9 is void as drafted.** *"não se responsabiliza por atrasos
decorrentes de exigências regulatórias ou medidas de compliance do provedor"* — CDC **art. 51 I** voids any clause
that exonerates or attenuates the supplier's liability toward a consumer, and **art. 14** makes a service supplier
objectively liable. CTech chose the provider; the consumer did not. The clause also buys nothing, because a
best-efforts-plus-information duty is what would be left standing anyway. Rewritten to promise notice, effort and
progress updates, with an express statement that CDC liability is not excluded.

**(b) A blanket no-refund clause is prohibited; the implementation is unresolved.** CDC art. 49 grants a
seven-day right to withdraw from a remote contract and requires immediate return of amounts paid; art. 51, II,
voids a clause that removes reimbursement where the CDC provides it. The statute does not itself specify the
accounting treatment for partly consumed virtual credits. Engineering must not invent that legal rule or quietly
break Invariant #6. **Paid sandbox sales remain disabled** until counsel defines the treatment of unused and used
credits and approves a bounded reversal that cannot create a cash-out or bypass gambling limits. The terms must
then match the implemented remedy exactly.

**(c) A `med_receivable` is not automatically a debt owed to CTech.** Compensation under CC arts. 368–369
requires reciprocal, liquid, due obligations of fungible things. The provider's MED debit does not by itself
prove that CTech is the user's creditor or may seize later deposits. Do not create, collect or compensate a
`med_receivable` until the Asaas contract identifies the loss bearer and counsel confirms the claim, notice,
contest and collection basis. In all cases, an undisputed balance above a valid claim must remain withdrawable;
CTech may not retain the whole account as security through a standard-form clause (CDC art. 51, IV).

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

**(i) The 2% withdrawal charge is a legal blocker, not a drafting problem.** Res. Conj. 16/2025, art. 8º, XI,
requires the BaaS contract to prohibit CTech from charging in its own name for Asaas's products or services.
Because this charge exists only on withdrawal and covers Asaas's transfer tariff, describing ledger,
reconciliation and support does not safely separate it from the payment service. Disable it as required by §6.4;
only written Asaas and counsel approval can restore a genuinely independent platform price.

**Technically coherent but still contract-dependent:** same-CPF-only withdrawals; refunding third parties in
full; crediting `netValue`; absorbing the account-opening cost; the 18+ gate; sandbox
non-convertibility; identifying Asaas; and pre-operation price/limit disclosure. Death is governed by CC
art. 682, **II**, not IV. Append-only audit improves integrity but does not supply a perpetual-retention basis
under LGPD arts. 15–16.

**Not yet technically coherent:** same-CPF-only deposits. `payment.customer` is a platform-linked customer
record, not proof of the actual Pix payer; §5.2.2 and §10 Q15 must be resolved before automatic credit.

---

## 10. Open questions — Asaas commercial/support, before implementation

1. ✅ **ANSWERED (2026-07-29): the free-Pix quota is per subaccount** — each subaccount has its own quota and its
   own management. Consequence: deposits and withdrawals are free for ordinary users; §6 becomes a guard for the
   tail, not the default experience; `min_deposit` needs no change. It does **not** become CTech margin while the
   proposed withdrawal charge remains disabled (§6.4).
2. ✅ **ANSWERED: BaaS subaccount holders have no panel/login/key access** — every movement is executed by the
   parent via API, and users interact only with the CTech wallet. This is the fact §5.4's internal hold rests on.
3. **Regulatory evaluation period:** exact limits for our account and how to clear it (§9.4). ⚠ Note this gates
   the **wallet** launch, not just poker: the caps are on *subaccount creation* (max 10 per distinct holder) and
   *R$2.000 per subaccount* over 60 days. Deferring poker does not defer this.
4. ✅ **ANSWERED FOR POKER (2026-07-30):** the account is approved and the user confirms that the Asaas contract
   does not prevent the described poker operation. The published Terms contain no express poker prohibition,
   while clauses 19.2 and 20 preserve Asaas's rights concerning illegality and commercial acceptance. This closes
   the provider-contract question, not the independent legal classification in §§0.2 and 14. **Dominó remains
   unconfirmed** unless it was included in the same disclosed and approved activity.
5. **BaaS subaccount `apiKey`:** confirmed returned at creation for BaaS clients, and rotation available without
   the 2-hour manual UI enablement?
6. ✅ **ANSWERED IN LAW:** Res. Conj. 16/2025, arts. 8º, §2º, and 14, I, require disclosure of the regulated
   provider and of CTech's non-regulated role. Ask Asaas only for the exact brand/CNPJ wording and placement it
   requires contractually.
7. **Does a subaccount Pix transfer ever require SMS authorization** (`authorized: false`)? If so, the automated
   withdrawal path is blocked and needs a different mechanism.
8. **Static QR limits** per subaccount (count, rate) — undocumented; the per-deposit QR strategy depends on it.
9. **Do subaccount events reach the parent's webhook**, or must webhooks be registered per subaccount (via the
   `webhooks` array at creation)? Affects the onboarding call shape.
10. **Internal transfer between two *sibling* subaccounts** — confirmed supported (§5.4), but is there a rate
    limit or a daily cap? A busy table close emits up to `N-1` legs at once.
11. **MED:** which webhook event announces a `Mecanismo Especial de Devolução` reversal on a subaccount, what
    notification window we get before the debit settles, and whether it can be contested with evidence (§5.7).
    Referenced by §5.7 step 1 — the whole `med_receivable` design depends on not learning about MED from a
    balance mismatch.
12. **Existing Asaas customer:** Terms clause 4.2 normally permits only one account per CPF and rejects duplicate
    registration data. Can an existing PF Asaas account be linked/migrated into this BaaS root, or must onboarding
    fail with a supported exit path? Do not create a second account or substitute a different document.
13. **PF account purpose:** the general Terms describe the Conta Asaas as intended for commercial purposes. Which
    BaaS addendum or approved commercial configuration governs a consumer player's PF subaccount, and does it
    override or particularize that wording for the disclosed use?
14. **Authoritative transfer tariff:** is there an API quote or a contractually fixed maximum available before a
    Pix transfer? Confirm whether `transferFee` is returned before final settlement and whether a free-quota
    counter is authoritative enough to disclose and reserve the exact user charge (§5.3).
15. **Actual Pix payer identity:** which API endpoint or re-queryable transaction field returns the real payer's
    CPF/CNPJ and is tied to `endToEndIdentifier`? `payment.customer` is insufficient because it identifies the
    customer record linked to the charge. Confirm refund mechanics when the authoritative payer differs from the
    subaccount holder.

---

## 11. Phasing

1. **Custody first, no gambling.** Onboarding + deposit + withdrawal on subaccounts; `GAMBLING_ENABLED=false`
   (already the default). Invariant #13 reconciliation job lands with it. Legal artifacts (§9.2, §9.3) published
   and gated before the first real user.
2. **Settlement.** §5.4 internal transfers, `settlement_pending_in/out`, `settleCustody` and drift alarms. Add a
   fixed platform-fee leg only after the legal answers in §§6 and 14. Then games.
3. **Decommission Inter.** Only after every pending deposit and `processing` withdrawal at Inter is resolved and
   the Inter balance is zero. Both integrations must coexist during migration — a spec of its own, including
   what happens to existing users whose money currently sits in the Inter omnibus account (they must be
   onboarded to a subaccount and their balance transferred **to their own account**, one at a time, audited).

## 12. Non-goals

- No change to the ledger, idempotency, locking, or three-wallet topology.
- No change to the responsible-gambling limit engine or the `real → game` choke point.
- No pot/omnibus account for tables (§5.4) — explicitly rejected.
- No Asaas Conta Escrow — `HoldGame` is internal and the user has no direct Asaas access (§§0.1 and 5.4).
- No CTech-billed payment fee (§6.2) — explicitly rejected.
- No migration mechanics for existing Inter balances (separate spec, §11.3).

---

## 13. Appendix — **withdrawn draft; do not apply or publish**

This appendix is retained only to show the text reviewed on 2026-07-30. It is **not legally corrected and must
not be copied into `ctech-account`**. In particular it still (a) charges CTech's 2% withdrawal fee despite Res.
Conj. 16/2025, art. 8º, XI; (b) assumes a CTech mandate is the correct source of instruction authority without
reconciling the direct Asaas-user contract required by art. 3º, IV; (c) promises MED debt/compensation before the
creditor and legal basis are established; and (d) states a sandbox refund solution not yet approved under the
financial invariants. Replace the entire appendix only after counsel returns an approved mandate/terms package
and Asaas approves it under the executed BaaS contract.

**Historical deployment notes — inactive.** `legalDocuments.wallet` currently holds **v2.1**
(`updatedAt: '25 de julho de 2026'`). Do not perform the steps below while this legal hold remains:

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

---

## 14. Questions to send directly to counsel

Solicitar parecer assinado que identifique a versão do produto, regras dos jogos, contratos e documentos
analisados. Cada resposta deve concluir `permitido`, `proibido` ou `permitido se`, citar a base legal e indicar a
alteração contratual ou operacional necessária. “Risco baixo” sem conclusão não é suficiente.

### 14.1 Premissas já confirmadas — não precisam ser investigadas novamente

1. A conta-raiz CNPJ da CTech possui aprovação cadastral e acesso à API Asaas.
2. A conta-raiz CNPJ pode criar subcontas com `cpfCnpj` e titularidade individual dos usuários PF.
3. O usuário não recebe painel, login, chave de API ou outro canal técnico direto de movimentação; utiliza apenas
   a CTech Wallet.
4. Os Termos Asaas apresentados não proíbem expressamente poker; isso é distinto da aprovação cadastral da
   conta-raiz e não substitui a análise de legalidade do jogo.
5. A arquitetura não utilizará Conta Escrow, conta de mesa, conta de pote ou conta omnibus da CTech.
6. O poker real é P2P, sem banca, sem rake percentual sobre o pote e com preço fixo de acesso por faixa.

### 14.2 Hold, dívida de jogo e liquidação — perguntas prioritárias

1. Considerando o **Código Civil, art. 814, caput e §§1º–3º**, o resultado de cada modalidade de poker constitui
   dívida inexigível de jogo, jogo legalmente permitido ou prêmio de competição esportiva/intelectual? O parecer
   deve explicar qual exceção permite — ou não permite — a liquidação automática, mesmo se o jogo não for
   contravenção penal.
2. O aceite do buy-in e a movimentação `available → held`, sem transferência bancária e mantendo o dinheiro na
   conta PF do jogador, constituem pagamento voluntário anterior ao jogo para fins do art. 814? Ou a transferência
   Asaas posterior ao resultado seria cobrança forçada de dívida de jogo? Indicar o momento jurídico exato em que
   o valor deixa de estar livremente disponível ao jogador.
3. Um mandato pode autorizar previamente a CTech a transferir o valor perdido depois do resultado, ou essa
   autorização seria contrato destinado a reconhecer, novar, garantir ou disfarçar dívida de jogo, alcançado pelo
   **art. 814, §1º**? A resposta deve considerar também os arts. 653, 661, §1º, 667 e 668 do Código Civil.
4. A CTech pode recusar saque, `game → real`, nova partida e encerramento sobre a parcela em `held` durante uma
   partida aceita pelo usuário? Definir hipóteses, prazo máximo, dever de informação, canal de contestação e
   tratamento de partida interrompida, fraude, erro, desconexão ou resultado impugnado à luz do CDC, especialmente
   arts. 6º, III, 14, 46, 51, IV, e 54, §4º.
5. Depois do resultado, os ganhos podem permanecer como `settlement_pending_in`, indisponíveis para saque e novo
   jogo até a transferência Asaas chegar a `DONE`? Essa indisponibilidade protege o consumidor ou configura
   retenção abusiva de saldo? Definir prazo, informação ao usuário e solução se a conta do perdedor for bloqueada.
6. A compensação e o netting multilateral de uma mesa podem gerar uma transferência de A para C quando não existe
   obrigação bilateral direta entre eles, apenas posições líquidas na mesma mesa? Validar a base nos arts. 368–380
   do Código Civil ou exigir que cada transferência siga exatamente a relação perdedor–vencedor registrada.
7. Quem suporta economicamente uma liquidação impossível porque a conta do perdedor foi bloqueada depois do
   resultado: perdedor, vencedor ou CTech? A CTech não pretende adiantar o prêmio nem ser contraparte; confirmar
   se essa regra é válida e como deve aparecer nos termos sem excluir responsabilidade inderrogável do CDC.

### 14.3 BaaS, titularidade e mandato

8. Os Termos Asaas, cláusulas 5.1.1–5.1.7, o cadastro `POST /v3/accounts` em CPF e o aceite versionado satisfazem
   a relação direta e a titularidade exigidas pela **Resolução Conjunta BCB/CMN 16/2025, arts. 3º, IV, e 4º,
   §§2º–4º**, apesar de a CTech manter a credencial e a interface exclusiva? Quais comprovantes devem ser
   conservados por usuário?
9. A interface exclusiva da CTech atende aos deveres de acesso a saldo, extrato, contratos, tarifas, atendimento,
   reclamação e encerramento ou algum desses direitos deve ser exercido diretamente perante a Asaas? Especificar
   as obrigações de cada parte conforme os arts. 9º, 14 e 16 da Resolução Conjunta 16/2025.
10. Criar contas, gerar QR Pix, iniciar saques de mesma titularidade, reservar saldo internamente e comandar
    transferências entre subcontas excede o papel de tomadora BaaS ou constitui alguma atividade do **art. 6º,
    III, da Lei 12.865/2013** que exija autorização própria?
11. Revisar e redigir o mandato separado exigido pelas cláusulas 5.1.6–5.1.7 dos Termos Asaas. Ele deve cumprir os
    arts. 654, §1º, 657, 661, §1º, 667, 668 e 682 do Código Civil, identificar poderes especiais, proibições,
    prestação de contas, revogação, assinatura eletrônica e evidências. A CTech pode aceitar a versão atual dos
    Termos Asaas em nome do usuário ou o próprio usuário precisa aceitá-la diretamente?
12. As cláusulas Asaas 5.1.4–5.1.5 tornam a CTech solidariamente responsável por obrigações das subcontas e
    garantidora de determinados saldos negativos. Quais perdas podem ser repassadas ao usuário, com contraditório,
    e quais devem ser suportadas exclusivamente pela CTech? Confirmar que dívida de uma subconta nunca pode ser
    coberta com dinheiro pertencente a outros usuários.

### 14.4 Legalidade e exploração dos jogos

13. Para **cada modalidade exata de poker**, considerando aleatoriedade das cartas, quantidade de mãos, duração,
    matchmaking, prêmio e preço de acesso, a habilidade predomina para o **Decreto-Lei 3.688/1941, art. 50,
    §3º, `a`**? Indicar jurisprudência atual e se é necessário laudo estatístico/pericial.
14. Responder separadamente à pergunta 13 para **cada modalidade de dominó**. Não reutilizar automaticamente a
    conclusão do poker.
15. Ainda que o produto esteja fora do escopo de eventos virtuais de quota fixa da Portaria SPA/MF 1.207/2024,
    qual norma oferece base afirmativa para sua exploração comercial online com dinheiro real? Se a Lei
    14.790/2023 for aplicável, confirmar as consequências dos arts. 4º, 6º, 21 e 21-A.
16. O preço fixo de acesso à mesa, sem percentual do pote, é remuneração lícita de serviço tecnológico ou ainda
    caracteriza exploração econômica do jogo? Quais fatos, limites, nota fiscal e textos de oferta são necessários
    para manter essa separação? Confirmar se qualquer rake real deve permanecer proibido.

### 14.5 Tarifas, consumidor, PLD, dados e tributos

17. A **Resolução Conjunta 16/2025, art. 8º, XI, e art. 15** proíbe a taxa CTech de saque de 2%, por ela nascer no
    uso do serviço Pix da Asaas? Se existir preço de plataforma juridicamente separado, identificar sua
    contraprestação real, forma de cobrança, possibilidade de percentual e limites do CDC, art. 51, IV e §1º.
18. Quais valores podem entrar na conta-raiz CTech sem violar o **art. 8º, XIV**, incluindo preço fixo de acesso,
    assinatura e outros serviços não financeiros? Informar documento fiscal, autorização e momento da
    transferência. Confirmar que depósitos, holds, potes e liquidações entre jogadores nunca passam pela conta
    CTech.
19. Como o **CDC, art. 49**, aplica-se a créditos sandbox pagos quando não usados, parcialmente usados ou já
    consumidos em jogo? Aprovar uma solução que não converta sandbox em dinheiro, não crie cash-out e não devolva
    limite de entrada `real → game`.
20. Enquanto o CPF/CNPJ do pagador real de um Pix não puder ser confirmado por fonte autoritativa, a CTech pode
    manter o recebimento indisponível em `payer_verification_pending`? Definir prazo máximo, aviso, contestação e
    devolução à origem conforme CDC e deveres de PLD/FTP; confirmar que `payment.customer` não basta como prova.
21. A CTech se enquadra diretamente na **Lei 9.613/1998, art. 9º, caput, I, ou parágrafo único, VI**? Se sim,
    listar cadastro, KYC complementar, PEP/sanções, registros, monitoramento, comunicação ao COAF e deveres dos
    arts. 10–11, distinguindo-os das responsabilidades da Asaas.
22. Após MED, chargeback ou estorno, quem é o credor jurídico do déficit? A CTech pode criar e compensar um
    `med_receivable` com depósitos futuros sob os arts. 368–369 do Código Civil? Definir prova, aviso, contestação,
    prescrição e parcela incontroversa que deve continuar disponível.
23. Para cada fluxo de dados, classificar Asaas e CTech como controladoras independentes, conjuntas ou
    controladora/operadora; indicar bases dos arts. 7º e 11 da LGPD, transparência, subprocessadores,
    transferências internacionais, incidentes e necessidade de RIPD.
24. Fornecer tabela de retenção para ledger, `wallet_audit`, KYC, biometria, webhooks, dispositivos, segurança,
    MED e credenciais. Para cada item, citar fundamento e prazo do **art. 16 da LGPD** e o evento de eliminação ou
    anonimização.
25. Em morte ou incapacidade, considerando **Código Civil, art. 682, II**, quais atos de preservação, informação,
    liquidação de partidas iniciadas e pagamento a sucessores ainda podem ser executados? Definir documentos e
    procedimento sem violar sigilo ou LGPD.
26. Após responder às questões 15–18, definir a tributação de cada receita da CTech e de ganhos entre jogadores:
    ISS, município competente, emissor e momento da nota, IR/IRRF, obrigações acessórias e eventual dever de
    informação ou retenção sobre liquidações P2P.
