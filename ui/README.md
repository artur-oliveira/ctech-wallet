# ctech-wallet UI

Next.js 16 **static-export SPA** (React 19) for the `ctech-wallet` API. Consumes
`api` over `/v1.0/*` **same-origin** (CloudFront forwards to the ALB; `next dev`
proxies). Auth via `@aoctech/auth-client` (OAuth2 PKCE, OIDC). All money is
**integer centavos** end-to-end — formatted for display only.

> **This app drives real money.** The 12 Financial Safety Invariants in the repo
> root [`../CLAUDE.md`](../CLAUDE.md) are non-negotiable. The UI's job is to
> never obscure where money is or nudge toward gambling (see `../ui/PRODUCT.md`).

## Stack

- **Next.js 16** `output: 'export'` — pure static build in prod; `next dev`
  proxies `/v1.0/*` to `DEV_API_ORIGIN` so the browser stays same-origin
  (`next.config.ts`).
- **UI:** ShadCN over Base UI (`@base-ui/react`), `lucide-react`, `react-hook-form`.
- **Server state:** TanStack Query (`@tanstack/react-query`).
- **i18n:** `react-i18next` + browser lang detect (**pt-BR** default, en first-class).
- **Realtime:** `@aoctech/ws-client` (`useWalletRealtime`).

## Routes (page → API)

| Route | File | Auth gate | Calls |
|--------|------|-----------|-------|
| `/` `/login` | `src/app/{pt-BR,en,login}/page.tsx` | public | `startOAuthFlow` |
| `/callback` | `src/app/callback/page.tsx` | — | `exchangeCode` → `doRefresh` |
| `/dashboard` | `src/app/dashboard/page.tsx` | protected | `me`, `getBalances`, `getLedger`, realtime |
| `/gambling/activate` | `src/app/gambling/activate/page.tsx` | protected (+ KYC) | `activateGambling` |
| `/gambling/responsible` | `src/app/gambling/responsible/page.tsx` | protected | `getGameLimits`/`setGameLimits`/`selfExclude` |

Localized layouts `src/app/{pt-BR,en}/layout.tsx` + `static-locale-boundary`.
`components/protected-route.tsx` gates authenticated pages. `terms-addendum-gate.tsx`
blocks the app until the current terms addendum is accepted.

## Components (`src/components/wallet/`)

`balance-cards` (real/game/sandbox — color encodes semantics), `ledger-list` +
`ledger-tabs`, `pix-charge-dialog` (opens deposit → QR), `amount-dialog`,
`confirm-money-dialog`, `money-receipt-dialog`, `transaction-status-list`. Shared:
`language-switcher`, `query-error-state`.

## Hooks / providers / auth

- **`src/lib/context/AuthContext.tsx` + `src/lib/hooks/useAuth.ts`** — app auth state.
- **`src/lib/providers/QueryProvider.tsx`** — TanStack Query cache.
- **`src/lib/providers/I18nProvider.tsx`** — i18n.
- **`src/lib/auth/oauth.ts`** — wraps `@aoctech/auth-client`:
  - `scope: 'openid profile kyc'` (`:11`).
  - `startOAuthFlow` (`:26`), `startStepUpFlow` (`:36`) → `startOAuthFlow(returnTo,{maxAge:0})`
    for withdrawal step-up (forces ctech-account to re-prove MFA, see root CLAUDE.md §Cross-project).
  - `doRefresh` (`:57`): refresh token is the **HttpOnly + SameSite `ctech_rt` cookie**
    set by ctech-account; JS never reads it.
  - `endSessionRedirect` (`:69`) ends the SSO session on logout.
- **Access token is in-memory only** (`client.ts:23`) — never persisted, never in a cookie.

## API client (`src/lib/api/client.ts`)

- **Same-origin**: `API_BASE_URL = NEXT_PUBLIC_API_URL ?? ''` (`:20`) → browser calls `/v1.0/*`.
- **Idempotency**: mutating calls send `Idempotency-Key` via `idemConfig` (`:115`).
- Method map (→ `api` routes): `me`→`GET /v1.0/auth/me`, `getBalances`→`GET /v1.0/wallet`,
  `createDeposit`→`POST /v1.0/wallet/deposits`, `createWithdrawal`→`POST /v1.0/wallet/withdrawals`,
  `purchaseSandbox`→`POST /v1.0/wallet/sandbox/purchase`, `activateGambling`→`POST /v1.0/wallet/gambling/activate`,
  `fundGame`→`POST /v1.0/wallet/game/deposit`, `returnFromGame`→`POST /v1.0/wallet/game/withdraw`,
  `getLedger`→`GET /v1.0/wallet/:type/ledger`.
- **401 interceptor** auto-refreshes via `registerRefreshFn` then retries, else re-auth.

## Realtime (`src/lib/hooks/useWalletRealtime.ts`)

WebSocket at `/v1.0/ws` (`:19`); the in-memory access JWT is passed **as the
first frame** (`authToken`, `:101`) — mirrors `api` `ws.go`. Events:
`deposit_confirmed` (invalidates balances/ledger + toast) and
`withdraw_completed` / `withdraw_reversed` / `withdraw_refund_failed`.

## Money constants (B18 — mirrored api↔ui by hand)

`FEE_ABSOLUTE_MIN = 100`, defaults `200/100/1000` (`src/lib/utils/fee.ts`);
`SANDBOX_CREDITS_PER_CENTAVO = 10` (`src/lib/utils/money.ts`). Keep in sync
with `api/internal/domain/wallet/{fee,model}.go` — `rpc-contract` defines **no**
money constants.

## Build / quality gate

```bash
npm ci && npm run build     # static export → out/
npx eslint src --ext .ts,.tsx   # MUST pass: zero errors, zero warnings
```

## Cross-links

- API (endpoint contract, invariants): [`../api/README.md`](../api/README.md),
  [`../api/ENDPOINTS.md`](../api/ENDPOINTS.md)
- Root: [`../CLAUDE.md`](../CLAUDE.md) (Financial Safety Invariants),
  [`../README.md`](../README.md)
- Infra: [`../cdk/README.md`](../cdk/README.md)
