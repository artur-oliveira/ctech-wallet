# CLAUDE.md — ctech-wallet (monorepo root)

Digital wallet for the `aoctech.app` platform. Three balances per user:

| Wallet    | Money    | Purpose                                            | Limits |
|-----------|----------|----------------------------------------------------|--------|
| `real`    | Real     | PIX deposit/withdraw, subscriptions, services       | No     |
| `game`    | **Real** | Real money ring-fenced for games only               | **Yes**|
| `sandbox` | Virtual  | Game credits; no monetary value, never convertible  | Yes    |

`game` holds **real money** — withdrawable via `real`, and it counts toward the user's real holdings. It exists so
personal gambling limits have exactly one edge to meter (`real → game`). Base for subscription billing and
skill-game (poker/dominó) integration.

**Before any task:** Read `docs/specs/2026-07-10-wallet-design.md` and
`docs/specs/2026-07-12-three-wallet-topology-design.md` (the latter supersedes the former's wallet types and
sandbox-purchase sections). This service custodies real third-party money — the Financial Safety Invariants below
are non-negotiable and override convenience.

---

## Projects

| Project | Role                                                        | Full guidelines   |
|---------|-------------------------------------------------------------|-------------------|
| `api/`  | Go REST API — Fiber v3, DynamoDB, Valkey, PIX (Inter)       | `api/CLAUDE.md`   |
| `ui/`   | Next.js 16 frontend — TypeScript, ShadCN                    | `ui/CLAUDE.md`    |
| `cdk/`  | AWS CDK infrastructure — TypeScript                         | `cdk/CLAUDE.md`   |

Structure and conventions mirror `ctech-dfe` (fx DI, layered `handler → service → repository`, RFC 7807, AWS SDK
v2, DynamoDB single-table helpers). **Always read the relevant subproject `CLAUDE.md` before making a change.**

Unlike `ctech-dfe`, the wallet is **not multi-tenant** — there is no organization header or RBAC. Access control
is: user JWT (JWKS from `ctech-account`) for user routes, `client_credentials` M2M scopes for internal routes, and
step-up MFA (`last_mfa_at` claim) for withdrawals.

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
5. **Cross-wallet ops take locks in a fixed order** (`real` → `game` → `sandbox`) to avoid deadlock.
   `lock.AcquireOrdered` sorts wallet IDs, so the order is total and deadlock-free for any number of wallets —
   always go through it rather than taking locks by hand.
6. **Sandbox never becomes real.** No withdrawal or conversion route accepts `type=sandbox`. Sandbox is a sink:
   nothing converts it back to `game` or `real`. Enforced at the handler, not just documented.
7. **Real money enters the gambling ring-fence ONLY via `real → game`.** There is no `real → sandbox` path;
   sandbox is bought from `game`. That single edge is where personal limits are enforced — add a second door and
   the limits enforce nothing. The regression test `TestSandboxPurchaseNeverDebitsRealWallet` is the executable
   form of this rule; if it ever fails, the limits are broken, whatever else passes.
8. **The `real → game` limit counts GROSS INFLOW.** A `game → real` return never refunds limit headroom.
   Otherwise a cap is churned around indefinitely (fund → return → fund). Returns are never limited and never
   charged: moving money *out* of the ring-fence reduces exposure, which is the behaviour limits exist to
   encourage.
9. **`game` balance is real money.** Withdrawable (via `real`), counts toward the user's real holdings, never
   expired or written off. The user's total real money is `real.balance + game.balance`.
10. **Consent is opt-in and auditable.** `game` does not exist until the user accepts the gambling
    addendum (a document distinct from the terms addendum) with KYC at least `basic` — `sandbox` is play currency
    and is created independently (lazily, on first use), with no KYC/consent requirement of its own.
    Activation, consent, and every
    personal-limit change append to `wallet_audit` — append-only, never updated, never deleted, enforced in IAM
    with an explicit DENY on `UpdateItem`/`DeleteItem`. Never fabricate consent: a legacy user holding a sandbox
    wallet from the old two-wallet model is **not** activated.
11. **The PIX webhook is never the source of truth for money movement.** A deposit credits only after
    re-querying the charge by `txid` at the Inter API (confirming amount and status). The webhook is a "wake
    up and re-check" signal for those, authenticated by a static `hmac` query parameter Inter echoes back on
    every callback. Payer CPF is the one field Inter's re-query does not return — it is sourced from the
    webhook body itself (persisted on first sight) and used only for the CPF-match anti-fraud check, never to
    authorize crediting.
12. **Deposits are custody-only.** A user deposit opens a PIX charge on that user's own BaaS subaccount,
    held under their CPF — never on CTech's Inter account. There is no fallback: no approved subaccount, no
    deposit (`409 wallet-onboarding`). CTech's Inter account receives **product purchases** only (SKU sales,
    `OpenCharge` invoices); CTech's **master account at the BaaS provider** receives the one-off subaccount
    verification fee, because that is the balance the provider debits its own charge from. The regression test
    `TestDepositNeverChargesInter` is the executable form of this rule. See
    `docs/specs/2026-08-30-asaas-only-deposits.md`.
13. **The verification fee is charged before the subaccount exists, and never refunded.** The provider
    consumes it at creation and does not return it when registration is refused, so a refused registration
    returns to `pending_documents` for re-submission — never to `closed`, which would strand a paid fee and
    force a second payment. The user is told this before paying.
14. **No money left in limbo.** A withdrawal whose PIX transfer call fails after the internal debit enters a
    `processing` state that a reconciliation job MUST resolve (complete or reverse). Failed refunds raise an
    operational alarm for manual reconciliation — never a silent path.

If a requested change appears to require breaking one of these, stop and ask.

---

## Universal Rules (apply to every project)

### DRY — think generic first

Before writing any function, search the codebase (`rg "..."`):
1. Reuse existing code. 2. Extend if reuse is insufficient. 3. Parameterize if behavior differs only by inputs.
4. Create new only when no suitable alternative exists. Two implementations of the same problem must be unified.

### Constants — no magic variables

Every string key, numeric code, URL, header name, scope, cache-key prefix, or enum value MUST be a named
constant. Header names, scope strings (`internal:wallet:credit`), and DynamoDB attribute names are defined once.

### Backend error handling

All API errors MUST be returned as RFC 7807 Problem JSON via the `problem.*` helpers (`sendProblem(c, err)`;
services return `*problem.Problem`). Never return raw errors, `fiber.Map`, or `fiber.NewError`. Match the
`ctech-account` / `ctech-dfe` problem type URIs and the wallet-specific codes: `insufficient-balance`,
`wallet-busy`, `withdraw-cpf-mismatch`, `kyc-not-verified`, `idempotency-conflict`, `step-up-required`.

### Frontend quality gate

`ui:` `npx eslint src --ext .ts,.tsx` must pass with **zero errors and zero warnings** before any commit.

### Money math

All balances and amounts are **integer centavos** — never floats.

**There is no withdrawal fee**, per `docs/specs/2026-08-16-withdrawal-fee-removal.md`. A withdrawal debits
exactly its `amount` and writes one `withdraw` ledger entry — no fee entry, no `fee_bps`/`fee_min`/`fee_max`
configuration, no sweep, no flag. Historical DynamoDB fee attributes and ledger rows are immutable history that
new code neither reads nor writes. An Asaas tariff is a provider cost, tracked separately as `transfer_fee`, and
is never CTech revenue.

**PIX deposit range** IS per-wallet: optional `min_deposit` / `max_deposit` overrides falling back to
the defaults (R$1 / R$10.000). The effective minimum is **never below the absolute floor of 100 centavos**, and an
incoherent override (`max < min`) widens rather than rejecting every amount. The range is checked *before*
`CreateCharge`, so a rejected amount never opens a charge at Inter.

Deposit-range fields are **admin-only** — set directly in DynamoDB; there is no API write path for them.

### Testing — core functions need integration tests

| Change             | Required tests                              |
|--------------------|---------------------------------------------|
| Ledger / balance   | Unit + integration (no-negative, atomic)    |
| Idempotency        | Unit + integration (replay = same result)   |
| Lock / concurrency | Integration (concurrent op → `wallet-busy`) |
| PIX flow           | Integration (webhook → re-query → credit)   |
| AWS integration    | Integration (DynamoDB-local)                |
| Bug fix            | Reproduce + regression                      |

Every core service function must have an integration test in addition to unit tests.

---

## Scope Control

Implement only what was requested. No unrelated fixes, opportunistic refactors, directory reorganization, or API
changes.

## Never Assume

Never assume DynamoDB table/index names, API contracts, PIX/Inter payload formats, scope strings, JWT claims, or
business rules. If not explicit: search codebase → search the spec/docs → search `ctech-account` → ask the user.

## Secrets

Never commit: JWT secrets, PIX/Inter mTLS certificates or client secrets, webhook secrets, AWS credentials,
passwords, real customer data, or real CPFs.

---

## Cross-project contract (`ctech-account`)

- **JWKS:** fetch from `{CTECH_URL}/.well-known/jwks.json`; RS256 only; verify `aud` contains the wallet's
  `SERVICE_AUDIENCE` and `iss` == `CTECH_URL`.
- **Access-token claims used:** `sub` (user_id), `scope`, `azp` (client_id), `kyc_level` (`""|basic|enhanced` —
  `wallet.KYCVerified == "enhanced"`, matching `kyc.LevelEnhanced` in `ctech-account`),
  `last_mfa_at` (unix, step-up freshness), empty `sid` marks an M2M `client_credentials` token.
- **KYC promotion:** on the first confirmed deposit call `POST {CTECH_URL}/v1.0/internal/kyc/confirm`
  `{user_id, cpf}` (idempotent; mismatch → 409). CPF for payer/withdrawal matching:
  `GET {CTECH_URL}/v1.0/internal/kyc/:user_id`.
- **Scopes:** `internal:wallet:credit` / `internal:wallet:debit` (sandbox only) and `internal:wallet:debit-real`
  (real wallet — deliberately a separate scope, never granted to a client that only needs sandbox credit/debit,
  e.g. poker/dominó) seeded into the global catalog via `ctech-account`'s `cmd/seedscopes`. The wallet's own
  M2M client is seeded confidential + `first_party:true` with `allowed_scopes:["internal:account:kyc"]`.
- **Step-up:** withdrawals mirror account's `RequireRecentMFA(5m)` — stateless, reads `last_mfa_at` from the JWT;
  no call to account needed. Re-verifying a stale MFA proof redirects to `{CTECH_URL}/v1.0/authorize` with
  `max_age=0` (OIDC-standard) — this forces `ctech-account` to require a fresh interactive login even with a valid
  SSO session, refreshing `last_mfa_at`. A plain re-login (no `max_age`) would silently reuse the SSO session and
  never re-prove MFA. The frontend calls this via `@aoctech/auth-client`'s `startOAuthFlow(returnTo, {maxAge: 0})`
  (wrapped as `startStepUpFlow` in `ui/src/lib/auth/oauth.ts`).

`ctech-account` DOES require code changes for this: `internal/handler/authorize.go` honors `max_age`, and
`ui/src/hooks/use-redirect-if-authenticated.ts` must not bypass the login form when the `continue` target itself
requests `max_age=0`. Beyond that, only operational seeding is needed.

---

## Mandatory Workflow

1. Read the design spec and the relevant subproject `CLAUDE.md`.
2. `rg "..."` — search for existing implementations before creating new code (reuse → extend → parameterize →
   create).
3. Plan → Implement (TDD for ledger/idempotency/locking) → Run affected tests.
4. Update docs: new endpoint/schema/scope → the spec or a technical doc; new constraint/workaround → note it.
5. Review cross-project impact (state which components were reviewed: `api` ↔ `ui` ↔ `cdk` ↔ `ctech-account`).
6. Suggest a Conventional Commit (`feat:` / `fix:` / `refactor:` / `docs:` / `chore:`, no emojis, no
   `Co-Authored-By` trailer).

## Mandatory Documentation Policy

**Every code change MUST be documented.**

There are NO exceptions.

Any modification affecting behavior, architecture, APIs, integrations, configuration, deployment, security, business rules, or developer workflow MUST include the corresponding documentation update in the same change.

---

## CTech Family — Cross-Repo Awareness (IMPORTANT)

This repo is one service in the CTech product family, not an isolated project. All CTech repos live under the same GitHub account and are meant to be treated as one codebase split across repos:

- ctech-cdk (github.com/artur-oliveira/ctech-cdk) — shared CDK constructs (EC2/ASG, DynamoDB, etc.)
- ctech-go-common (github.com/artur-oliveira/ctech-go-common) — shared Go libraries (HTTP client, auth, retries, websocket drain, caching)
- ctech-account, ctech-wallet, ctech-billing, ctech-dfe, ctech-poker — backend services
- ctech-ui (github.com/artur-oliveira/ctech-ui) — shared frontend design system / components (adoption in progress)
- ctech-ws-client (github.com/artur-oliveira/ctech-ws-client) — shared websocket client library
- ctech-oauth-client, ctech-vanity, ctech-lbalancer — supporting infra/clients

Before making a decision here, ask: "does this apply to the whole family, not just this repo?" Treat as cross-repo by default:
- Infra/runtime bugs (clock drift, spot interruption handling, websocket draining, health checks, load balancer behavior) — check ctech-cdk / ctech-lbalancer and sibling services for the same exposure before treating it as local.
- API leaks/perf/cost bugs (DynamoDB read/write amplification, KMS decrypt calls, SQS growth, N+1 requests) — check whether the root cause is shared code (ctech-go-common) or a repeatable pattern other services also have.
- Frontend state/websocket/resilience/UX patterns (reconnect, circuit breaker, error/loading/empty states, 404/500/503 pages, OAuth flow, modals, buttons) — check ctech-ui and ctech-ws-client for the shared version before implementing locally.
- New reusable code (not service-specific business logic) — default to proposing it for a shared package (ctech-cdk, ctech-go-common, ctech-ui, ctech-ws-client) instead of duplicating it here.

A fix scoped to only this repo, for a problem that is actually systemic across the family, is an incomplete fix. This applies to AI agents working in single-repo sessions too.

ctech-wallet is a payments/money-movement service: its idempotency discipline (every mutation keyed, transactional ledger+guard writes, replay-safe) and its fencing/locking discipline (Valkey-backed ordered wallet locks with safe in-memory fallback only in dev) are the gold standard the rest of the family should be measured against — before copying a lock or idempotency pattern from elsewhere into a money path, check whether this repo already does it better.
