# CLAUDE.md — ui (ctech-wallet-ui)

Next.js 16 (static export) + React 19 frontend for the ctech-wallet API.

## Stack

- **Framework:** Next.js 16 `output: 'export'` with `images: {unoptimized: true}`
  (mandatory — the default image loader needs a server the export has none of).
  Deployed to **Cloudflare Workers Static Assets**, not S3+CloudFront. `next dev`
  still proxies `/v1.0/*` to `DEV_API_ORIGIN`, which is now the only place dev and
  prod differ.
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

- **Cross-origin** in deployed environments: nothing proxies `/v1.0/*` at the edge any
  more, so the browser calls `NEXT_PUBLIC_API_URL` directly and **CORS applies**. Only
  `next dev` is same-origin, via the rewrite. Empty `NEXT_PUBLIC_API_URL` = same-origin
  and is a local-development value only.
- **Idempotency:** mutating calls send an `Idempotency-Key` header
  (`src/lib/api/client.ts` `idemConfig`).
- **Realtime:** WebSocket at `/v1.0/ws` (`useWalletRealtime`); the in-memory access JWT
  is passed as the auth token (first frame) via `@aoctech/ws-client`. Its origin comes
  from **`NEXT_PUBLIC_WS_URL`**, read rather than derived: the deployed CSP's
  `connect-src` is generated from the `https://`/`wss://` literals in the build
  environment, and `connect-src` is scheme-exact — allowing `https://host` does not
  allow `wss://host`.

## Locale routes

`/`, `/en` and `/pt-BR` are three plain exported pages; the CloudFront Function that
redirected `/` to a locale is gone. `/` prerenders pt-BR and switches client-side from
the stored preference or `Accept-Language`; `/en` and `/pt-BR` are pinned by
`StaticLocaleBoundary`. hreflang lives on the three **pages**, never the root layout —
metadata is inherited, so a canonical there would also claim to be `/dashboard`'s.

## Rules

- Keep the access token in memory; never move it to storage or a readable cookie.
- Every new external origin the app talks to must appear as a literal in the caller's
  `build-env-*` (or `extra-connect-src`), or the generated `connect-src` blocks it.
- Money is integer **centavos** end to end; format for display only.

## Mandatory Documentation Policy

**Every code change MUST be documented.**

There are NO exceptions.

Any modification affecting behavior, architecture, APIs, integrations, configuration, deployment, security, business rules, or developer workflow MUST include the corresponding documentation update in the same change.
