# CLAUDE.md — ctech-wallet (monorepo root)

Digital wallet for the `aoctech.app` platform. Three balances per user:

| Wallet    | Money    | Purpose                                            | Limits  |
|-----------|----------|----------------------------------------------------|---------|
| `real`    | Real     | PIX deposit/withdraw, subscriptions, services      | No      |
| `game`    | **Real** | Real money ring-fenced for games only              | **Yes** |
| `sandbox` | Virtual  | Game credits; no monetary value, never convertible | Yes     |

`game` holds **real money** — withdrawable via `real`, and it counts toward the user's real holdings. It exists so
personal gambling limits have exactly one edge to meter (`real → game`). Base for subscription billing and
skill-game (poker/dominó) integration.

**Before any task:** Read `docs/specs/2026-07-10-wallet-design.md` and
`docs/specs/2026-07-12-three-wallet-topology-design.md` (the latter supersedes the former's wallet types and
sandbox-purchase sections). This service custodies real third-party money — the Financial Safety Invariants below
are non-negotiable and override convenience.

---

## Projects

| Project | Role                                                  | Full guidelines |
|---------|-------------------------------------------------------|-----------------|
| `api/`  | Go REST API — Fiber v3, DynamoDB, Valkey, PIX (Inter) | `api/CLAUDE.md` |
| `ui/`   | Next.js 16 frontend — TypeScript, ShadCN              | `ui/CLAUDE.md`  |
| `cdk/`  | AWS CDK infrastructure — TypeScript                   | `cdk/CLAUDE.md` |

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
10. **Consent is opt-in and auditable.** `game`/`sandbox` do not exist until the user accepts the gambling
    addendum (a document distinct from the terms addendum) with verified KYC. Activation, consent, and every
    personal-limit change append to `wallet_audit` — append-only, never updated, never deleted, enforced in IAM
    with an explicit DENY on `UpdateItem`/`DeleteItem`. Never fabricate consent: a legacy user holding a sandbox
    wallet from the old two-wallet model is **not** activated.
11. **The PIX webhook is never the source of truth for money movement.** A deposit credits only after
    re-querying the charge by `txid` at the Inter API (confirming amount and status). The webhook is a "wake
    up and re-check" signal for those, authenticated by a static `hmac` query parameter Inter echoes back on
    every callback. Payer CPF is the one field Inter's re-query does not return — it is sourced from the
    webhook body itself (persisted on first sight) and used only for the CPF-match anti-fraud check, never to
    authorize crediting.
12. **No money left in limbo.** A withdrawal whose PIX transfer call fails after the internal debit enters a
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

**Withdrawal fee** is per-wallet: each `wallets` row may carry optional `fee_bps` / `fee_min` / `fee_max`
overrides, each falling back to the defaults (2% / R$1 / R$10) when unset. The effective fee is
`clamp(amount*bps/10000, min, max)` in integer arithmetic and **never below the absolute floor of 100 centavos**
regardless of overrides.

**PIX deposit range** is per-wallet the same way: optional `min_deposit` / `max_deposit` overrides falling back to
the defaults (R$1 / R$10.000). The effective minimum is **never below the absolute floor of 100 centavos**, and an
incoherent override (`max < min`) widens rather than rejecting every amount. The range is checked *before*
`CreateCharge`, so a rejected amount never opens a charge at Inter.

Fee and deposit-range fields are **admin-only** — set directly in DynamoDB; there is no API write path for them.

Transfers between `real` and `game` carry **no fee** in either direction.

### Testing — core functions need integration tests

| Change             | Required tests                              |
|--------------------|---------------------------------------------|
| Fee calculation    | Unit (min/max boundaries)                   |
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
- **Access-token claims used:** `sub` (user_id), `scope`, `azp` (client_id), `kyc_level` (`""|basic|verified`),
  `last_mfa_at` (unix, step-up freshness), empty `sid` marks an M2M `client_credentials` token.
- **KYC promotion:** on the first confirmed deposit call `POST {CTECH_URL}/v1.0/internal/kyc/confirm`
  `{user_id, cpf}` (idempotent; mismatch → 409). CPF for payer/withdrawal matching:
  `GET {CTECH_URL}/v1.0/internal/kyc/:user_id`.
- **Scopes:** `internal:wallet:credit` / `internal:wallet:debit` (sandbox only) and `internal:wallet:real:debit`
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

# CLAUDE.md

Behavioral guidelines to reduce common LLM coding mistakes. Merge with project-specific instructions as needed.

**Tradeoff:** These guidelines bias toward caution over speed. For trivial tasks, use judgment.

## 1. Think Before Coding

**Don't assume. Don't hide confusion. Surface tradeoffs.**

Before implementing:

- State your assumptions explicitly. If uncertain, ask.
- If multiple interpretations exist, present them - don't pick silently.
- If a simpler approach exists, say so. Push back when warranted.
- If something is unclear, stop. Name what's confusing. Ask.

## 2. Simplicity First

**Minimum code that solves the problem. Nothing speculative.**

- No features beyond what was asked.
- No abstractions for single-use code.
- No "flexibility" or "configurability" that wasn't requested.
- No error handling for impossible scenarios.
- If you write 200 lines and it could be 50, rewrite it.

Ask yourself: "Would a senior engineer say this is overcomplicated?" If yes, simplify.

## 3. Surgical Changes

**Touch only what you must. Clean up only your own mess.**

When editing existing code:

- Don't "improve" adjacent code, comments, or formatting.
- Don't refactor things that aren't broken.
- Match existing style, even if you'd do it differently.
- If you notice unrelated dead code, mention it - don't delete it.

When your changes create orphans:

- Remove imports/variables/functions that YOUR changes made unused.
- Don't remove pre-existing dead code unless asked.

The test: Every changed line should trace directly to the user's request.

## 4. Goal-Driven Execution

**Define success criteria. Loop until verified.**

Transform tasks into verifiable goals:

- "Add validation" → "Write tests for invalid inputs, then make them pass"
- "Fix the bug" → "Write a test that reproduces it, then make it pass"
- "Refactor X" → "Ensure tests pass before and after"

For multi-step tasks, state a brief plan:

```
1. [Step] → verify: [check]
2. [Step] → verify: [check]
3. [Step] → verify: [check]
```

Strong success criteria let you loop independently. Weak criteria ("make it work") require constant clarification.

---

**These guidelines are working if:** fewer unnecessary changes in diffs, fewer rewrites due to overcomplication, and
clarifying questions come before implementation rather than after mistakes.
