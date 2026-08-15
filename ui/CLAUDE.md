# CLAUDE.md — ui (ctech-wallet-ui)

Next.js 16 (static export) + React 19 frontend for the ctech-wallet API.

## Stack

- **Framework:** Next.js 16 `output: 'export'` (pure static build in prod; `next dev`
  proxies `/v1.0/*` to `DEV_API_ORIGIN` so the browser stays same-origin).
- **UI:** ShadCN on top of Base UI (`@base-ui/react`), `lucide-react` icons, `react-hook-form`.
- **Data:** TanStack Query (`@tanstack/react-query`) for server state.
- **i18n:** `react-i18next` + `i18next-browser-languagedetector` (pt-BR default).

## Auth (`@aoctech/auth-client`)

- OAuth **PKCE** (`Authorization Code + code_challenge`). `src/lib/auth/oauth.ts` wraps
  the SDK's `startOAuthFlow` / `startStepUpFlow` (`maxAge: 0` for step-up).
- The authorization request includes `openid profile kyc` and all public active
  `wallet:*` scopes from `src/lib/auth/scopes.ts`; its contract test reads the
  API manifest so the UI cannot silently drift from the Resource Server.
- **Refresh token** lives only in the HttpOnly + SameSite `ctech_rt` cookie set by
  `ctech-account`; JS never sees it.
- **Access token** is held **in memory only** (never persisted, never in a cookie).
- App state via `AuthContext` (`src/lib/context/AuthContext.tsx`) + `useAuth()` hook;
  `protected-route.tsx` gates authenticated pages.

## API access

- **Same-origin** in all environments — browser calls `/v1.0/*` (CloudFront forwards to
  the ALB; dev proxies it). `NEXT_PUBLIC_API_URL` only overrides the origin (and the WS
  origin); empty = same-origin.
- **Idempotency:** mutating calls send an `Idempotency-Key` header
  (`src/lib/api/client.ts` `idemConfig`).
- **Realtime:** WebSocket at `/v1.0/ws` (`useWalletRealtime`); the in-memory access JWT
  is passed as the auth token (first frame) via `@aoctech/ws-client`.

## Rules

- Keep the access token in memory; never move it to storage or a readable cookie.
- Never call the API cross-origin — keep `/v1.0/*` same-origin so CORS never applies.
- Money is integer **centavos** end to end; format for display only.

## Mandatory Documentation Policy

**Every code change MUST be documented.**

There are NO exceptions.

Any modification affecting behavior, architecture, APIs, integrations, configuration, deployment, security, business rules, or developer workflow MUST include the corresponding documentation update in the same change.
