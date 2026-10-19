# AGENTS.md — ui (ctech-wallet-ui)

Context for AI agents working on the `ctech-wallet` frontend. Same intent
as [`CLAUDE.md`](CLAUDE.md) (read it for resilience, locale routes and statements); this file is the agent-facing summary.
Family frontend standard: the `ctech-dev-standards` skill, `references/frontend.md`.

Next.js 16 **static-export SPA** (React 19) for the wallet API. This app
drives **real third-party money** — the 14 Financial Safety Invariants in the
repo root [`../CLAUDE.md`](../CLAUDE.md) are non-negotiable and override
convenience.

## Stack (confirmed in code)

- `next.config.ts` → `output: 'export'` in prod; `rewrites()` (DEV_API_ORIGIN
  proxy) only under `next dev`. The two are mutually exclusive by design.
- Auth: `@aoctech/auth-client` (OAuth2 PKCE). `src/lib/auth/oauth.ts`:
  scope assembled from the identity scopes plus every public `wallet:*` scope
  in `src/lib/auth/scopes.ts`; `startOAuthFlow`. Withdrawals need step-up
  (`startStepUpFlow`, `max_age=0`); Pix deposits/withdrawals of the `real` wallet go through the API (Billing). Refresh token is the **HttpOnly + SameSite `ctech_rt`
  cookie namespaced by OAuth client** ; the **access token is in-memory only**
  (`src/lib/api/client.ts`), never persisted.
- API access is **cross-origin** in deployed environments: the browser calls
  `NEXT_PUBLIC_API_URL` directly (`client.ts`) and **CORS applies**. Only
  `next dev` is same-origin, via the `next.config.ts` rewrite.
- Realtime: `src/lib/hooks/useWalletRealtime.ts` — WS at `/v1.0/ws` ,
  JWT passed as the **first frame** (`authToken`); events
  `deposit_confirmed`, `withdraw_completed`/`withdraw_reversed`/`withdraw_refund_failed`.

## Rules (MUST follow)

- **Never** move the access token to storage or a readable cookie.
- Treat `OAuthTransientError` as retryable and preserve local identity; only a
  `null` refresh result is a definitive absent/rejected session.
- **Never** talk to an origin that is not a literal in the caller workflow's
  `build-env-*` — the generated CSP `connect-src` is derived from those and will
  block anything else. `wss://` counts separately from `https://`.
- All money is **integer centavos** end-to-end; format for display only.
- Mutating calls MUST send an `Idempotency-Key` header (`idemConfig`,
  `client.ts:115`) — never omit it on POSTs that move money.
- i18n: no hardcoded strings; pt-BR default, en first-class.
- `npm run lint` MUST pass with zero errors/warnings.

## Known gaps vs the standard

- `@aoctech/ui` is not used (not in `package.json`); tests run on `node --test`, not vitest.
- Money constants (`SANDBOX_CREDITS_PER_CENTAVO` = 100, `MAX_AMOUNT_CENTS`) are pinned to `../rpc-contract/money.json` by
  `money-contract.test.mjs`. No fee constants exist (`../docs/specs/2026-08-16-withdrawal-fee-removal.md`).

## Cross-links

- API: [`../api/README.md`](../api/README.md), [`../api/ENDPOINTS.md`](../api/ENDPOINTS.md)
- Root invariants: [`../CLAUDE.md`](../CLAUDE.md)
- Infra: [`../cdk/README.md`](../cdk/README.md)

<!-- BEGIN:nextjs-agent-rules -->

# This is NOT the Next.js you know

This version has breaking changes — APIs, conventions, and file structure may all differ from your training data. Read the relevant guide in `node_modules/next/dist/docs/` (resolved from this file's directory; in monorepos the `next` package may not be visible from the repo root) before writing any code. Heed deprecation notices.

This block is written and re-added by `next dev` — verify at `node_modules/next/dist/server/lib/generate-agent-files.js`. Removing it from a diff only re-creates the uncommitted change; committing it with your work keeps the tree clean.

<!-- END:nextjs-agent-rules -->
