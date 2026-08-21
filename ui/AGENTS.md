# AGENTS.md — ui (ctech-wallet-ui)

Context for AI agents working on the `ctech-wallet` frontend. Identical intent
to [`CLAUDE.md`](CLAUDE.md); this file is the agent-facing summary.

Next.js 16 **static-export SPA** (React 19) for the wallet API. This app
drives **real third-party money** — the 12 Financial Safety Invariants in the
repo root [`../CLAUDE.md`](../CLAUDE.md) are non-negotiable and override
convenience.

## Stack (confirmed in code)

- `next.config.ts` → `output: 'export'` in prod; `rewrites()` (DEV_API_ORIGIN
  proxy) only under `next dev`. The two are mutually exclusive by design.
- Auth: `@aoctech/auth-client` (OAuth2 PKCE). `src/lib/auth/oauth.ts`:
  scope assembled from the identity scopes plus every public `wallet:*` scope
  in `src/lib/auth/scopes.ts`; `startOAuthFlow` (`:26`);
  `startStepUpFlow` (`:36`) → `startOAuthFlow(returnTo,{maxAge:0})` for the
  withdrawal step-up. Refresh token is the **HttpOnly + SameSite `ctech_rt`
  cookie** (`:48-50,57`); the **access token is in-memory only**
  (`src/lib/api/client.ts:23`), never persisted.
- API access is **cross-origin** in deployed environments: the browser calls
  `NEXT_PUBLIC_API_URL` directly (`client.ts:20`) and **CORS applies**. Only
  `next dev` is same-origin, via the `next.config.ts` rewrite.
- Realtime: `src/lib/hooks/useWalletRealtime.ts` — WS at `/v1.0/ws` (`:19`),
  JWT passed as the **first frame** (`authToken`, `:101`); events
  `deposit_confirmed`, `withdraw_completed`/`withdraw_reversed`/`withdraw_refund_failed`.

## Rules (MUST follow)

- **Never** move the access token to storage or a readable cookie.
- **Never** talk to an origin that is not a literal in the caller workflow's
  `build-env-*` — the generated CSP `connect-src` is derived from those and will
  block anything else. `wss://` counts separately from `https://`.
- All money is **integer centavos** end-to-end; format for display only.
- Mutating calls MUST send an `Idempotency-Key` header (`idemConfig`,
  `client.ts:115`) — never omit it on POSTs that move money.
- i18n: no hardcoded strings; pt-BR default, en first-class.
- `npx eslint src --ext .ts,.tsx` MUST pass with zero errors/warnings.

## Known divergences

- **B18** — money constants mirrored api↔ui by hand:
  `SANDBOX_CREDITS_PER_CENTAVO=10` (`src/lib/utils/money.ts`). `rpc-contract`
  defines NONE. Keep in sync manually. No fee constants exist — see
  `../docs/specs/2026-08-16-withdrawal-fee-removal.md`.

## Cross-links

- API: [`../api/README.md`](../api/README.md), [`../api/ENDPOINTS.md`](../api/ENDPOINTS.md)
- Root invariants: [`../CLAUDE.md`](../CLAUDE.md)
- Infra: [`../cdk/README.md`](../cdk/README.md)

## Mandatory Documentation Policy

**Every code change MUST be documented.**

There are NO exceptions.

Any modification affecting behavior, architecture, APIs, integrations, configuration, deployment, security, business rules, or developer workflow MUST include the corresponding documentation update in the same change.
