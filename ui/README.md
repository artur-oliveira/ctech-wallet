# ctech-wallet UI

Next.js 16.3.1 **static-export SPA** (React 19.2.8) for the `ctech-wallet` API,
deployed to **Cloudflare Workers Static Assets**. Consumes `api` at
`NEXT_PUBLIC_API_URL` **cross-origin** — nothing proxies `/v1.0/*` at the edge, so
**CORS applies**; only `next dev` is same-origin, via a rewrite. Auth via
`@aoctech/auth-client` (OAuth2 PKCE, OIDC). All money is **integer centavos**
end-to-end — formatted for display only. Family frontend standard: `ctech-dev-standards` skill,
`references/frontend.md`. The app does not use `@aoctech/ui` yet (not in `package.json`) and tests run on `node --test`.

> **This app drives real money.** The 14 Financial Safety Invariants in the repo
> root [`../CLAUDE.md`](../CLAUDE.md) are non-negotiable. The UI's job is to
> never obscure where money is or nudge toward gambling (see `../ui/PRODUCT.md`).

## Stack

- **Next.js 16** `output: 'export'` + `images: {unoptimized: true}` — pure static
  build in prod (the default image loader needs a server the export has none of, so
  the build fails without it); `next dev` proxies `/v1.0/*` to `DEV_API_ORIGIN`,
  the one place dev and prod differ on purpose (`next.config.ts`).
- **UI:** ShadCN over Base UI (`@base-ui/react`), `lucide-react`, `react-hook-form`.
- **Server state:** TanStack Query (`@tanstack/react-query`).
- **i18n:** `react-i18next` + browser lang detect (**pt-BR** default, en first-class).
- **Realtime:** `@aoctech/ws-client` (`useWalletRealtime`), origin from
  `NEXT_PUBLIC_WS_URL` — read, not derived from `NEXT_PUBLIC_API_URL`, because the
  deployed CSP's `connect-src` is generated from the build environment's literals
  and is scheme-exact (`https://host` does not permit `wss://host`).

## Routes (page → API)

| Route | File | Auth gate | Calls |
|--------|------|-----------|-------|
| `/` `/en` `/pt-BR` `/login` | `src/components/home.tsx`, `src/app/{en,pt-BR,login}/page.tsx` | public | `startOAuthFlow` |
| `/callback` | `src/app/callback/page.tsx` | — | `exchangeCode` → `doRefresh` |
| `/dashboard` | `src/app/dashboard/page.tsx` | protected | `me`, `getBalances`, `getLedger`, purchases history, realtime |
| `/unavailable` | `src/app/unavailable/page.tsx` | public | liveness probe; returns the user when the API is back |

The gambling pages (`/gambling/*`) are gone: consent, limits and self-exclusion are Poker's.

Localized layouts `src/app/{pt-BR,en}/layout.tsx` + `static-locale-boundary`. All three
homepage routes render `src/components/home.tsx`; the markup lives there and not in
`src/app/page.tsx` so `/` can export `metadata` (a client component cannot). Each of the
three declares its own hreflang alternates — never the root layout, whose metadata every
route inherits. There is no longer an edge redirect from `/` to a locale: `/` prerenders
pt-BR and switches client-side.
`components/protected-route.tsx` gates authenticated pages. `terms-addendum-gate.tsx`
blocks the app until the current terms addendum is accepted.

## Components (`src/components/wallet/`)

`balance-cards` (real and sandbox; color encodes semantics), `ledger-list` +
`ledger-tabs` (Real, Sandbox when it exists, Compras), `purchases-panel` (old Sandbox-pack and product purchases, read-only),
`amount-dialog`, `confirm-money-dialog`, `money-receipt-dialog`. `pix-charge-dialog` shows the Pix QR code of a deposit
(the invoice is opened at Billing). Pix deposit and withdrawal of the `real` wallet are live (daily limits enforced by the API);
withdrawals use the step-up re-verify in `confirm-money-dialog` (`startStepUpFlow`, `max_age=0`). Shared:
`language-switcher`, `query-error-state`.

## Hooks / providers / auth

- **`src/lib/context/AuthContext.tsx` + `src/lib/hooks/useAuth.ts`** — app auth state.
- **`src/lib/providers/QueryProvider.tsx`** — TanStack Query cache.
- **`src/lib/providers/I18nProvider.tsx`** — i18n.
- **`src/lib/auth/oauth.ts`** — wraps `@aoctech/auth-client`:
  - requests `openid profile kyc` plus every public active `wallet:*` scope
    defined in `src/lib/auth/scopes.ts`; a contract test pins that list to the
    API's Resource Server manifest.
  - `startOAuthFlow`, and `startStepUpFlow` for withdrawals (`max_age=0`).
  - `doRefresh`: refresh token is the **HttpOnly + SameSite `ctech_rt` cookie**
    set by ctech-account; JS never reads it.
  - `endSessionRedirect` ends the SSO session on logout.
- **Access token is in-memory only** (`client.ts`) — never persisted, never in a cookie.

## API client (`src/lib/api/client.ts`)

- **Cross-origin**: `API_BASE_URL = NEXT_PUBLIC_API_URL ?? ''`; deployed environments always set it, so calls go to the API host and CORS applies. The `''` fallback is `next dev` only.
- **Idempotency**: mutating calls send `Idempotency-Key` via `idemConfig`.
- Method map (→ `api` routes): `me`→`GET /v1.0/auth/me`, `acceptTermsAddendum`→`POST /v1.0/auth/terms-addendum/accept`,
  `getBalances`→`GET /v1.0/wallet`, `createDeposit`→`POST /v1.0/wallet/deposits`,
  `createWithdrawal`→`POST /v1.0/wallet/withdrawals`, `getLedger`→`GET /v1.0/wallet/:type/ledger`,
  `getSandboxPurchases`→`GET /v1.0/wallet/sandbox/purchases`, `getProductPurchases`→`GET /v1.0/wallet/product-purchases`.
- **401 interceptor** auto-refreshes via `registerRefreshFn` then retries, else re-auth.

## Realtime (`src/lib/hooks/useWalletRealtime.ts`)

WebSocket at `/v1.0/ws`; the in-memory access JWT is passed **as the
first frame** (`authToken`) — mirrors `api` `ws.go`. Events:
`deposit_confirmed` (invalidates balances/ledger + toast) and
`withdraw_completed` / `withdraw_reversed` / `withdraw_refund_failed` (toast only).

## Money constants

`SANDBOX_CREDITS_PER_CENTAVO` (100) and `MAX_AMOUNT_CENTS` are in `src/lib/utils/money-constants.ts`, pinned to
`../rpc-contract/money.json` by `money-contract.test.mjs` (the Go side is pinned by `rpc-contract/money_test.go`). There are no
fee constants: see `../docs/specs/2026-08-16-withdrawal-fee-removal.md`.

## Build / quality gate

```bash
npm ci && npm run build     # static export → out/
npm run lint                # MUST pass: zero errors, zero warnings
npm test
npm audit --omit=dev        # MUST report zero production vulnerabilities
```

## Cross-links

- API (endpoint contract, invariants): [`../api/README.md`](../api/README.md),
  [`../api/ENDPOINTS.md`](../api/ENDPOINTS.md)
- Root: [`../CLAUDE.md`](../CLAUDE.md) (Financial Safety Invariants),
  [`../README.md`](../README.md)
- Infra: [`../cdk/README.md`](../cdk/README.md)
