# CLAUDE.md — ctech-wallet (monorepo root)

## CTech Standards (mandatory)

Before writing, changing or reviewing any code or infra here, load the `ctech-dev-standards` skill (plugin
`ctech-skills`, repo github.com/artur-oliveira/ctech-skills). It is the single source of truth for backend,
frontend and infra (patterns, resilience, scale, cost). If anything in this file contradicts it, the skill
wins and this file must be fixed. Install on a new machine:
`/plugin marketplace add artur-oliveira/ctech-skills` then `/plugin install ctech-skills@ctech-skills`.

Digital wallet for the `aoctech.app` platform. **A ledger, not a payment gateway.** Two balances per user:

| Wallet    | Money   | Purpose                                                              |
|-----------|---------|----------------------------------------------------------------------|
| `real`    | Real    | Subscriptions, services, games stake it; Pix in and out (via Billing) |
| `sandbox` | Virtual | Game credits; no monetary value, never convertible. Poker sells and spends them. |

The wallet holds balances and keeps the books. It does **not** talk to Inter, open charges, sell products, or
own any gambling rule: **ctech-billing** owns the Inter connection (deposits are Billing invoices, withdrawals
are Billing payouts), **ctech-poker** owns consent, personal limits, self-exclusion and the SKUs. The game wallet
that used to sit between them was merged into `real` (2026-10); old `game` rows stay as retired, read-only data.

**Before any task:** Read `docs/specs/2026-10-14-wallet-ledger-billing-rail.md` **in the `ctech-billing` repo** (it is not
in this one; it is the authority for what the wallet is now). The older specs (`2026-07-10-wallet-design.md`, `2026-07-12-three-wallet-topology-design.md`)
describe the system before that and are history. This service custodies real third-party money, so the Financial
Safety Invariants below are non-negotiable and override convenience.

---

## Projects

| Project | Role                                                  | Full guidelines |
|---------|-------------------------------------------------------|-----------------|
| `api/`  | Go REST API — Fiber v3, DynamoDB, Valkey; rail = Billing | `api/CLAUDE.md` |
| `ui/`   | Next.js 16 static-export SPA — TypeScript, on Cloudflare Workers Static Assets | `ui/CLAUDE.md`  |
| `cdk/`  | AWS CDK infrastructure — TypeScript                   | `cdk/CLAUDE.md` |
| `rpc-contract/` | Go module: money constants shared by api and ui (`money.json`) | `rpc-contract/README.md` |

Layering, fx DI, RFC 7807, resilience and infra rules follow the family standard (the skill above); this file only holds
what is specific to the wallet. **Always read the relevant subproject `CLAUDE.md` before making a change.**

Unlike `ctech-dfe`, the wallet is **not multi-tenant** — there is no organization header or RBAC. Access control
is: user JWT (JWKS from `ctech-account`) for user routes, `client_credentials` M2M scopes for internal routes. Step-up MFA (`last_mfa_at` claim) is
required by `POST /wallet/withdrawals` (see the step-up contract below).

---

## Financial Safety Invariants (MUST hold — no exceptions)

These are the reason the service exists. A change that weakens any of them is wrong regardless of how convenient
it is.

1. **Balance never goes negative.** Every debit carries `ConditionExpression: balance >= :amount`. Failure →
   `409 insufficient-balance`. Never read-then-write balance; use conditional `TransactWriteItems`.
2. **Ledger is append-only.** Authoritative balance lives in `wallets` (atomic counter). `ledger_entries` is an
   immutable audit trail — never updated, never deleted, never used to derive balance.
3. **Every operation is idempotent.** All mutations require an idempotency key (`Idempotency-Key` header for
   user routes; `txid` or `wallet_id#round_id` for internal). Replay with the same key returns the prior result
   and never duplicates a ledger entry. Enforced by a unique GSI on `idempotency_key`.
4. **One operation per wallet at a time.** Serialize via Valkey `SETNX wallet:{id}` with a short TTL (10s) +
   retry/backoff. Fail safe if the process dies (TTL releases the lock). Contention → `409 wallet-busy`.
5. **Cross-wallet ops take locks in one fixed total order** to avoid deadlock.
   `lock.AcquireOrdered` (wraps `api-commons/lock`) sorts the `wallet:{id}` keys lexicographically, so the order is total
   and deadlock-free for any number of wallets and does not depend on wallet type — always go through it rather than
   taking locks by hand.
6. **Sandbox never becomes real, and real never becomes Sandbox.** No withdrawal or conversion route accepts
   `type=sandbox`, and no operation debits `real` to credit Sandbox: Sandbox is bought from Poker with a Pix paid
   through Billing, and nothing converts it back. Enforced by `TestNoRouteConvertsSandboxToAnything` (the list of
   writing routes is closed on purpose), `TestSandboxHasNoRealDebitPath` and `TestCreditingSandboxNeverDebitsReal`.
7. *(Retired 2026-10-15 — "real money enters the ring-fence only via `real → game`"; the `game` wallet is gone.
   Spec `2026-10-14-wallet-ledger-billing-rail.md` §9.)*
8. *(Retired 2026-10-15 — the `real → game` gross-inflow limit; personal limits are Poker's now.)*
9. *(Retired 2026-10-15 — "`game` balance is real money"; there is one money balance, `real`.)*
10. *(Retired 2026-10-15 — consent and the audit of limit changes moved to Poker, which keeps them append-only. The
    wallet's own audit of money movements is unchanged. Self-exclusion state recorded before the move stays on the
    user row; a hold is still refused while one is recorded, fail closed.)*
11. **A Billing event is never the source of truth for money movement.** A deposit credits only after the wallet
    re-reads the invoice from Billing and checks its own rules; the signed `invoice.paid` event is a "wake up and
    re-check" signal (the pending-deposit sweep covers a lost one). Payer data is Billing's and write-once.
12. **Deposits are credited only for a payer the wallet can attribute.** A paid deposit is credited only when the
    amount equals the expected amount **and** the payer matches the KYC CPF by the wallet's own check **and** Billing
    says `payer_match = match`. Any disagreement quarantines the deposit (never credits) and refunds the payer; a
    paid deposit with no payer identity yet stays pending (an `ALARM` line is logged on each sweep). Every payout
    goes only to the CPF on the user's KYC record (the client never supplies a destination). Daily limits apply per
    wallet. Custody on CTech's Inter account is an explicit, accepted risk for a closed group of KYC-approved users
    (`docs/specs/2026-10-08-wallet-restoration-design.md`).
13. *(Retired 2026-10-07 — was the BaaS verification-fee rule; see `docs/specs/2026-10-07-asaas-removal.md`.)*
14. **No money left in limbo.** A withdrawal whose Billing payout call fails or times out after the internal debit stays
    `processing`, and a reconciliation job MUST resolve it from Billing (complete or reverse). A lookup that fails is never
    read as "not found", so a transport error never reverses a withdrawal. Failed refunds and failed credit-backs log an
    `ALARM` line for manual reconciliation — never a silent path (no SNS alert yet; see "Known gaps vs the standard").

If a requested change appears to require breaking one of these, stop and ask.

---

## Wallet-specific rules

Generic rules (DRY, named constants, RFC 7807 via go-common, zero-warning lint, tests with the change, scope control,
never assume, no secrets, documentation with the change, family-first) live in the `ctech-dev-standards` skill; they are
not repeated here. The wallet's own error codes: `insufficient-balance`, `wallet-busy`, `withdraw-cpf-mismatch`,
`kyc-not-verified`, `idempotency-conflict`, `step-up-required`, `daily-deposit-limit`, `daily-withdraw-limit`
(`api/internal/walletproblem` and go-common `problem`). Never commit PIX/Inter certificates, webhook secrets or real CPFs.

### Money math

All balances and amounts are **integer centavos** — never floats. Sandbox credits are whole numbers; the rate
(`SANDBOX_CREDITS_PER_CENTAVO`) and `MAX_AMOUNT_CENTS` live in `rpc-contract/money.json` and are pinned by a Go test and
a UI test.

**There is no withdrawal fee**, per `docs/specs/2026-08-16-withdrawal-fee-removal.md`. A withdrawal debits
exactly its `amount` and writes one `withdraw` ledger entry — no fee entry, no `fee_bps`/`fee_min`/`fee_max`
configuration, no sweep, no flag. Historical DynamoDB fee attributes and ledger rows are immutable history that
new code neither reads nor writes. A payment-provider tariff is a provider cost and
is never CTech revenue.

**PIX deposit range** IS per-wallet: optional `min_deposit` / `max_deposit` overrides falling back to
the defaults (R$1 / R$10.000). The effective minimum is **never below the absolute floor of 100 centavos**, and an
incoherent override (`max < min`) widens rather than rejecting every amount. The range is checked *before*
any charge is opened at the provider, so a rejected amount never opens one.

**Daily PIX limits** are per-wallet too, all in centavos/counts and falling back to defaults: `daily_deposit_cap`
(R$1.000, GROSS inflow per São Paulo calendar day, enforced when the deposit is credited; any excess is refunded
to the payer), `daily_withdraw_cap` (R$1.000) and `daily_withdraw_count` (1). They are checked under the real
wallet lock with an optimistic counter written in the same transaction as the money movement. A reversed
withdrawal returns its slot; a refunded or reversed deposit never frees deposit headroom.

Deposit-range and daily-limit fields are **admin-only** — set directly in DynamoDB; there is no API write path for them.

### Testing — core functions need integration tests

| Change             | Required tests                              |
|--------------------|---------------------------------------------|
| Ledger / balance   | Unit + integration (no-negative, atomic)    |
| Idempotency        | Unit + integration (replay = same result)   |
| Lock / concurrency | Integration (concurrent op → `wallet-busy`) |
| Deposit flow       | Integration (Billing event → re-read → credit) |
| AWS integration    | Integration (DynamoDB-local)                |
| Bug fix            | Reproduce + regression                      |

Every core service function must have an integration test in addition to unit tests.

---

## Cross-project contract (`ctech-account`)

- **JWKS:** fetch from `{CTECH_URL}/.well-known/jwks.json`; RS256 only; verify `aud` contains the wallet's
  `SERVICE_AUDIENCE` and `iss` == `CTECH_URL`.
- **Access-token claims used:** `sub` (user_id), `scope`, `azp` (client_id), `kyc_level` (`""|basic|enhanced` —
  `wallet.KYCVerified == "enhanced"`, matching `kyc.LevelEnhanced` in `ctech-account`),
  `last_mfa_at` (unix, step-up freshness), empty `sid` marks an M2M `client_credentials` token.
- **KYC is read-only here:** promotion is manual-review-only on account's side. The wallet only reads the verified CPF
  and level for payer/withdrawal matching: `GET {CTECH_URL}/v1.0/internal/kyc/:user_id` (scope `internal:account:kyc`,
  the wallet's own M2M client; `api/internal/kycclient`).
- **Scopes:** the manifest `api/internal/oauthresource/scope-manifest.json` is the source of truth and is published to
  `ctech-account` by the deploy (see `OPERATIONS.md`). Active internal scopes: `internal:wallet:credit` /
  `internal:wallet:debit` (sandbox only), `internal:wallet:debit-real` (real wallet — deliberately a separate scope,
  never granted to a client that only needs sandbox credit/debit), `internal:wallet:hold` (Poker's holds/cash-out on
  `real`), `internal:wallet:balance`, `internal:wallet:erasure-eligibility`. Public: the six `wallet:*` scopes. Others in
  the manifest are `deprecated` (retired routes).
- **Step-up:** `POST /wallet/withdrawals` requires it via `middleware.RequireRecentMFA(StepUpMaxAge)` (5 min), mirroring
  account's `RequireRecentMFA` — stateless, reads `last_mfa_at` from the JWT; no call to account needed.
  Re-verifying a stale MFA proof redirects to `{CTECH_URL}/v1.0/authorize` with `max_age=0` (OIDC-standard) — this
  forces `ctech-account` to require a fresh interactive login even with a valid SSO session, refreshing
  `last_mfa_at`. A plain re-login (no `max_age`) would silently reuse the SSO session and never re-prove MFA. The
  frontend calls `@aoctech/auth-client`'s `startOAuthFlow(returnTo, {maxAge: 0})` (`startStepUpFlow`). Account honours
  `max_age` in `internal/handler/authorize.go`.

- **Account deletion (LGPD):** the wallet is a saga participant
  (`docs/plans/2026-10-07-account-deletion-participant.md`).
  - It serves `GET /v1.0/internal/erasure/eligibility/:sub` (scope `internal:wallet:erasure-eligibility`, token
    minted by account).
  - It consumes `{env}-ctech-wallet-erasure` (SNS filter `services=wallet`) and acks with `internal:account:erasure-ack`
    through a **dedicated** confidential client (`ERASURE_CLIENT_ID`), never the KYC client.
  - It reads the JWT revocation list from Valkey DB 0 (`VALKEY_REVOCATION_URL`).
  - From the start of grace, every operation that opens new exposure for the user is refused (`checkErasure`);
    settlement is never refused.
  - Retained PII goes to the write-only `wallet_erasure_retention` table. The ledger is never touched.
  - `cpf_hmac` comes from account's internal KYC response and keys carried self-exclusions.

---

## Mandatory Workflow

1. Read the design spec and the relevant subproject `CLAUDE.md`.
2. `rg "..."` — search for existing implementations before creating new code.
3. Plan → Implement (TDD for ledger/idempotency/locking) → Run affected tests.
4. Update docs with the change (specs in `docs/specs/`, plans in `docs/plans/`).
5. Review cross-project impact (state which components were reviewed: `api` ↔ `ui` ↔ `cdk` ↔ `ctech-account`).
6. Suggest a Conventional Commit (`feat:` / `fix:` / `refactor:` / `docs:` / `chore:`, no emojis, no trailers).

ctech-wallet is the family's reference for money movement: its idempotency discipline (every mutation keyed,
transactional ledger+guard writes, replay-safe) and its locking discipline (Valkey-backed ordered wallet locks, in-memory
fallback only in dev) are what other services are measured against. Before copying a lock or idempotency pattern from
elsewhere into a money path, check whether this repo already does it better.

---

## Known gaps vs the standard (code, not docs; do not "fix" by editing docs)

- Local `internal/walletproblem` (wallet types on top of go-common `problem`) and local `internal/middleware` (auth, scope, step-up, revocation) instead of go-common's guards.
- App-level rate limiting (go-common `ratelimit`, `api/internal/api/v1/ratelimit.go`) per user (JWT sub): reads 120/min FailOpen; deposits and withdrawals 10/min FailClosed (503 if Valkey is down). Retired 410 and internal M2M routes are not limited. nginx `limit_req` stays at the edge.
- No go-common `alerts` (SNS): failures that need a human are `ALARM` log lines only (reconcile, quarantined deposits,
  stale holds).
- DynamoDB GSIs project `ALL`; `cdk/lib/dynamodb-stack.ts` has no `KEYS_ONLY`/`INCLUDE`.
- The UI does not use `@aoctech/ui` (not in `ui/package.json`); its tests run on `node --test`, not vitest.
