# Wallet UI: Infinite Statements, Purchases Tab and Copy Diet Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wallet statements load by infinite scroll and show description plus balance before and after; purchase history becomes a fourth tab (Compras) with Sandbox credits and Digital products as two filters; every "—" (em dash) in user-facing copy is gone; on-screen text is cut down in favor of self-explanatory elements.

**Architecture:** The data layer already paginates (`useInfiniteQuery` with cursors). This plan swaps the "Load more" buttons for an `IntersectionObserver` sentinel (pattern from `ctech-poker` `hands/page.tsx`, factored into one reusable hook plus one pure decision function so it is unit-testable), generalizes the tab model from `WalletType` to `WalletType | 'purchases'`, and replaces the standalone `PurchaseHistory` section with a `PurchasesPanel` rendered inside the tabs. `ctech-ui` has no infinite-list primitive today, so the hook lives here; proposing it upstream is a follow-up, not part of this plan.

**Tech Stack:** Next.js 16, React, TypeScript, TanStack Query v5, react-i18next, ShadCN/Base UI, Node's built-in test runner (`.mjs` tests).

**Spec:** `docs/specs/2026-10-08-wallet-restoration-design.md` (section 4). Depends on `docs/plans/2026-10-08-statement-api-balance-before.md` (the API field `balance_before`) and, for the dashboard file, on `docs/plans/2026-10-08-wallet-inter-rail-and-limits.md` Task 10. Execute those first, or expect a merge in `ui/src/app/dashboard/page.tsx` and the locale files.

## Global Constraints

- `cd ui && npx eslint src --ext .ts,.tsx` must pass with ZERO errors and ZERO warnings before every commit; `npx tsc --noEmit` must pass.
- No em dash (U+2014, "—") in any user-facing string (locale JSON values, JSX text, string literals rendered to the user). Separate with a bullet "•" or pause with ";". The true minus sign "−" (U+2212) used by `formatSigned` is NOT an em dash and stays. Code comments are out of scope.
- Both locales (`pt-BR.json`, `en.json`) get identical key sets. No hardcoded user text in components.
- Money formatting only through `formatBRL` / `formatCreditsAmount` / `formatSigned` in `lib/utils/money.ts`. Sandbox amounts carry no currency symbol (`walletHasMonetaryValue`).
- Prefer self-describing elements (labels, icons with `aria-label`, numbers) over sentences. A new string is added only when no existing element can carry the meaning.
- Keep the keyboard and ARIA tab contract (`role="tablist"`, roving `tabIndex`, Arrow/Home/End).
- `useInfiniteQuery` must not refetch every loaded page on window focus (poker's "carregou mais sozinho" lesson). Check `ui/src/lib/providers/QueryProvider.tsx` default `refetchOnWindowFocus`; set it to `false` on these queries if the default is true.
- Conventional Commits, no emojis, NO attribution trailers. Work on a branch (`feat/ui-infinite-statements`).

## Review Focus

- The sentinel stays in view after a page is appended (short pages, tall monitors): the observer must re-arm after each page so the next page loads without a scroll, but it must stop when `has_next` is false or a page fetch errored (no request loop on a failing endpoint).
- A failed "next page" must keep the rows already loaded and show a retry button, not replace the list with an error.
- Switching tabs must not leave a stale observer or a request in flight updating the wrong list (only the selected panel is mounted).
- The Compras tab must appear for users with no activated gambling wallet and no sandbox, and its two filters must each show their own empty state.
- Sandbox rows: `balance_before` and `balance_after` render as plain integers (credits), never with "R$".
- Long descriptions (255 chars) clamp to two lines and do not break the row height; a row with no description (legacy entries) keeps the type label as its main line.

---

### Task 1: Pure scroll decision and the sentinel hook

**Files:**
- Create: `ui/src/lib/utils/infinite-scroll.ts`
- Create: `ui/src/lib/utils/infinite-scroll.test.mjs`
- Create: `ui/src/lib/hooks/useInfiniteSentinel.ts`

**Interfaces:**
- Produces:
  - `INFINITE_SCROLL_ROOT_MARGIN = '400px 0px'`
  - `shouldLoadMore(s: {isIntersecting: boolean; hasNextPage: boolean; isFetching: boolean; hasError: boolean}): boolean`
  - `useInfiniteSentinel(opts: {hasNextPage: boolean; isFetching: boolean; hasError: boolean; itemCount: number; fetchNextPage: () => void}): (node: HTMLElement | null) => void` (a callback ref to attach to the sentinel element)

- [ ] **Step 1: Branch and write the failing test**

```bash
cd /home/artur-revgas/Documents/Projects/Ctech/ctech-wallet
git checkout main && git pull origin main && git checkout -b feat/ui-infinite-statements
```

`ui/src/lib/utils/infinite-scroll.test.mjs`:

```js
import assert from 'node:assert/strict'
import test from 'node:test'

import {INFINITE_SCROLL_ROOT_MARGIN, shouldLoadMore} from './infinite-scroll.ts'

const base = {isIntersecting: true, hasNextPage: true, isFetching: false, hasError: false}

test('loads the next page only when the sentinel is visible and more pages exist', () => {
  assert.equal(shouldLoadMore(base), true)
  assert.equal(shouldLoadMore({...base, isIntersecting: false}), false)
  assert.equal(shouldLoadMore({...base, hasNextPage: false}), false)
})

test('never loads while a request is in flight or after an error', () => {
  assert.equal(shouldLoadMore({...base, isFetching: true}), false)
  assert.equal(shouldLoadMore({...base, hasError: true}), false)
})

test('the margin prefetches before the user reaches the end', () => {
  assert.match(INFINITE_SCROLL_ROOT_MARGIN, /^\d+px 0px$/)
})
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd ui && node --disable-warning=MODULE_TYPELESS_PACKAGE_JSON --test --experimental-test-isolation=none src/lib/utils/infinite-scroll.test.mjs`
Expected: FAIL (module not found).

- [ ] **Step 3: Implement**

`ui/src/lib/utils/infinite-scroll.ts`:

```ts
/** Prefetch the next page this far before the sentinel enters the viewport. */
export const INFINITE_SCROLL_ROOT_MARGIN = '400px 0px'

interface LoadMoreState {
  isIntersecting: boolean
  hasNextPage: boolean
  isFetching: boolean
  /** A failed page fetch stops the auto-loading; the user retries explicitly. */
  hasError: boolean
}

/** Pure decision behind the sentinel observer, kept separate so it is testable. */
export function shouldLoadMore(s: LoadMoreState): boolean {
  return s.isIntersecting && s.hasNextPage && !s.isFetching && !s.hasError
}
```

`ui/src/lib/hooks/useInfiniteSentinel.ts`:

```ts
'use client'

import {useEffect, useState} from 'react'
import {INFINITE_SCROLL_ROOT_MARGIN, shouldLoadMore} from '@/lib/utils/infinite-scroll'

interface UseInfiniteSentinelOptions {
  hasNextPage: boolean
  isFetching: boolean
  hasError: boolean
  /** Rows currently shown. Changing it re-arms the observer, so a sentinel that
   *  is still in view after an append immediately asks for the next page. */
  itemCount: number
  fetchNextPage: () => void
}

/**
 * Returns a callback ref for a sentinel element placed after the last row.
 * When the sentinel nears the viewport the next page is fetched, until pages
 * run out or a fetch fails (then the caller shows a retry button).
 */
export function useInfiniteSentinel({
                                      hasNextPage,
                                      isFetching,
                                      hasError,
                                      itemCount,
                                      fetchNextPage,
                                    }: UseInfiniteSentinelOptions): (node: HTMLElement | null) => void {
  const [node, setNode] = useState<HTMLElement | null>(null)

  useEffect(() => {
    if (!node || !hasNextPage || hasError) return undefined
    const observer = new IntersectionObserver(
      (entries) => {
        if (shouldLoadMore({isIntersecting: !!entries[0]?.isIntersecting, hasNextPage, isFetching, hasError})) {
          fetchNextPage()
        }
      },
      {rootMargin: INFINITE_SCROLL_ROOT_MARGIN},
    )
    observer.observe(node)
    return () => observer.disconnect()
  }, [node, hasNextPage, hasError, isFetching, itemCount, fetchNextPage])

  return setNode
}
```

- [ ] **Step 4: Run, lint, commit**

Run: the node test above -> PASS; `cd ui && npx eslint src --ext .ts,.tsx && npx tsc --noEmit` -> clean.

```bash
git add ui && git commit -m "feat(ui): add an infinite-scroll sentinel hook"
```

---

### Task 2: Statement rows with description and balance before/after, infinite

**Files:**
- Modify: `ui/src/lib/types/api.ts` (`LedgerEntry`)
- Modify: `ui/src/lib/mock.ts` (mock ledger entries get `balance_before`)
- Modify (rewrite): `ui/src/components/wallet/ledger-list.tsx`
- Modify: `ui/src/lib/utils/money.ts` (add `formatBalance`)
- Test: `ui/src/lib/utils/money.test.mjs`, `ui/src/components/wallet/ledger-rows.test.mjs` (create), `ui/src/components/ui/ui-hardening.test.mjs` (only if it pins removed markup)

**Interfaces:**
- Consumes: `useInfiniteSentinel` (Task 1), API field `balance_before` (statement plan), `walletHasMonetaryValue`, `formatSigned`.
- Produces: `formatBalance(amount: number, monetary: boolean, locale?: string): string` (BRL for money wallets, plain grouped integer for sandbox); the new list UI.

- [ ] **Step 1: Write the failing tests**

Append to `ui/src/lib/utils/money.test.mjs` (read its imports first; follow them):

```js
test('formatBalance shows BRL for money wallets and bare credits for sandbox', () => {
  assert.match(formatBalance(123456, true, 'pt-BR'), /R\$\s?1\.234,56/)
  assert.equal(formatBalance(12000, false, 'pt-BR').includes('R$'), false)
  assert.match(formatBalance(12000, false, 'pt-BR'), /12\.000/)
})
```

`ui/src/components/wallet/ledger-rows.test.mjs`:

```js
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import test from 'node:test'

const listSource = await readFile(new URL('./ledger-list.tsx', import.meta.url), 'utf8')
const typesSource = await readFile(new URL('../../lib/types/api.ts', import.meta.url), 'utf8')

test('ledger rows show balance before and after from the server', () => {
  assert.match(typesSource, /balance_before: number/)
  assert.match(listSource, /entry\.balance_before/)
  assert.match(listSource, /entry\.balance_after/)
  assert.match(listSource, /ledger\.balance\.before/)
  assert.match(listSource, /ledger\.balance\.after/)
})

test('the statement loads by infinite scroll instead of a load-more button', () => {
  assert.match(listSource, /useInfiniteSentinel/)
  assert.equal(/dashboard\.ledger\.loadMore['"`]/.test(listSource), false)
})

test('a failed next page keeps the rows and offers a retry', () => {
  assert.match(listSource, /isFetchNextPageError/)
  assert.match(listSource, /common\.retry/)
})
```

- [ ] **Step 2: Run to verify they fail**

Run: `cd ui && npm test 2>&1 | tail -30` -> FAIL on the new assertions.

- [ ] **Step 3: Types, formatter, mock**

`api.ts`: in `LedgerEntry` add after `amount`:

```ts
  balance_before: number
```
and change the `description` comment to "Free-form text supplied by the service that moved the money."

`money.ts`, next to `formatSigned`:

```ts
/** Wallet balance → "R$ 1.234,56" for money wallets, or "12.000" for sandbox credits. */
export function formatBalance(amount: number, monetary: boolean, locale: string = i18n.language || 'pt-BR'): string {
  return monetary ? formatBRL(amount, locale) : formatCreditsAmount(amount, locale)
}
```

`mock.ts`: wherever ledger entries are built, add `balance_before: <balance_after - amount>` (compute from the neighbouring fields; the compiler lists each site).

- [ ] **Step 4: Rewrite `ledger-list.tsx`**

Keep `ledgerType`, the loading/error/empty branches and the query exactly as they are; replace the row markup and the load-more footer. Full replacement of the part after `const items = ...`:

```tsx
  const sentinelRef = useInfiniteSentinel({
    hasNextPage: !!hasNextPage,
    isFetching,
    hasError: isFetchNextPageError,
    itemCount: items.length,
    fetchNextPage: () => void fetchNextPage(),
  })

  return (
    <>
      <ul className="divide-y divide-border">
        {items.map((entry) => {
          const label = t(`ledger.type.${ledgerType(type, entry.type, entry.ref)}`, entry.type)
          return (
            <li key={entry.entry_id} className="flex items-start justify-between gap-4 px-5 py-3.5">
              <div className="min-w-0">
                {/* The service-supplied description is what the user recognises,
                    so it leads; the type label drops to the second line. */}
                <p className="line-clamp-2 text-sm font-medium text-foreground">{entry.description || label}</p>
                <p className="mt-0.5 text-xs text-muted-foreground">
                  {entry.description ? `${label} • ${dateFmt.format(new Date(entry.created_at))}` : dateFmt.format(new Date(entry.created_at))}
                </p>
              </div>
              <div className="shrink-0 text-right">
                <p className={`font-mono text-sm tabular-nums ${entry.amount < 0 ? 'text-muted-foreground' : 'text-brand-700'}`}>
                  {formatSigned(entry.amount, monetary)}
                </p>
                <p className="mt-0.5 font-mono text-xs tabular-nums text-muted-foreground">
                  <span className="sr-only">{t('ledger.balance.before')} </span>
                  {formatBalance(entry.balance_before, monetary)}
                  <span aria-hidden="true"> → </span>
                  <span className="sr-only"> {t('ledger.balance.after')} </span>
                  {formatBalance(entry.balance_after, monetary)}
                </p>
              </div>
            </li>
          )
        })}
      </ul>

      {isFetchNextPageError && (
        <div className="border-t border-border p-4 text-center">
          <p role="alert" className="mb-3 text-sm text-destructive">
            {t('dashboard.ledger.loadMoreError')}
          </p>
          <Button variant="outline" onClick={() => void fetchNextPage()} disabled={isFetchingNextPage}>
            {t('common.retry')}
          </Button>
        </div>
      )}

      {hasNextPage && !isFetchNextPageError && (
        <div ref={sentinelRef} className="p-4 text-center" aria-hidden={!isFetchingNextPage}>
          {isFetchingNextPage && (
            <span role="status" className="text-sm text-muted-foreground">
              {t('dashboard.ledger.loadingMore')}
            </span>
          )}
        </div>
      )}
    </>
  )
```

Add imports: `useInfiniteSentinel` from `@/lib/hooks/useInfiniteSentinel`, `formatBalance` next to `formatSigned`. Remove the now-unused `dashboard.ledger.loadMore` usage. Verify `common.retry` exists in both locales (`rg '"retry"' ui/src/locales`); if not, add it ("Tentar de novo" / "Try again").

If `QueryProvider` defaults `refetchOnWindowFocus` to true, add `refetchOnWindowFocus: false` to this `useInfiniteQuery` call with a one-line comment.

- [ ] **Step 5: Locales**

Add to both locale files (same key paths):

```json
"ledger": { "balance": { "before": "Saldo anterior", "after": "Saldo atual" } }
```
(en: "Previous balance", "Balance after"). Merge into the existing `ledger` object, do not replace it. Delete `dashboard.ledger.loadMore` from both files (no longer used).

- [ ] **Step 6: Verify and commit**

Run: `cd ui && npx eslint src --ext .ts,.tsx && npx tsc --noEmit && npm test 2>&1 | tail -20` -> clean, tests PASS.

Manual: `npm run dev`, mock mode; scroll the Real statement until all mock pages load; resize to a tall viewport and confirm pages keep loading without scrolling.

```bash
git add ui && git commit -m "feat(ui): infinite scroll statements with description and balance before and after"
```

---

### Task 3: Purchases become the fourth tab

**Files:**
- Modify: `ui/src/lib/utils/ledger-tabs.ts` (+ `ledger-tabs.test.mjs`)
- Modify: `ui/src/components/wallet/ledger-tabs.tsx`
- Create (replaces `purchase-history.tsx`): `ui/src/components/wallet/purchases-panel.tsx`
- Delete: `ui/src/components/wallet/purchase-history.tsx`
- Modify: `ui/src/app/dashboard/page.tsx` (drop `PurchaseHistory` import and `<PurchaseHistory/>`)
- Modify tests that pin the old structure: `ui/src/lib/utils/purchase-history.test.mjs`, `ui/src/lib/utils/ledger-visibility.test.mjs`, `ui/src/components/ui/ui-adaptation.test.mjs`

**Interfaces:**
- Consumes: `useInfiniteSentinel`, `apiClient.getSandboxPurchases(cursor)` / `getProductPurchases(cursor)`, `formatBRL`, `formatCreditsAmount`.
- Produces:
  - `export type LedgerTab = WalletType | 'purchases'`, `export const PURCHASES_TAB = 'purchases'`
  - `nextLedgerTab<T extends string>(tabs: T[], current: T, key: string): T | null`
  - `availableLedgerTabs(activated: boolean, hasSandbox: boolean): LedgerTab[]` (always ends with `'purchases'`)
  - `<PurchasesPanel/>` with two filters (`sandbox`, `product`).

- [ ] **Step 1: Update the tests first (they pin the old structure)**

`ledger-tabs.test.mjs`: add a case with the new tab set and keep the existing ones:

```js
const withPurchases = ['real', 'game', 'sandbox', 'purchases']

test('purchases participates in arrow, Home and End navigation', () => {
  assert.equal(nextLedgerTab(withPurchases, 'sandbox', 'ArrowRight'), 'purchases')
  assert.equal(nextLedgerTab(withPurchases, 'purchases', 'ArrowRight'), 'real')
  assert.equal(nextLedgerTab(withPurchases, 'game', 'End'), 'purchases')
})
```

`ledger-visibility.test.mjs`, replace the first test:

```js
test('an existing sandbox statement stays visible without gambling activation', () => {
  assert.match(tabsSource, /hasSandbox \? \['real', 'sandbox'\] : \['real'\]/)
  assert.match(tabsSource, /\[\.\.\.wallets, PURCHASES_TAB\]/)
  assert.match(tabsSource, /!activated && hasSandbox && selectedTab === 'sandbox'/)
  assert.match(dashboardSource, /hasSandbox=\{!!balances\.data\.sandbox\}/)
})
```

`purchase-history.test.mjs` (rewrite; keep its file name or rename to `purchases-panel.test.mjs`):

```js
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import test from 'node:test'

const dashboardSource = await readFile(new URL('../../app/dashboard/page.tsx', import.meta.url), 'utf8')
const tabsSource = await readFile(new URL('../../components/wallet/ledger-tabs.tsx', import.meta.url), 'utf8')
const panelSource = await readFile(new URL('../../components/wallet/purchases-panel.tsx', import.meta.url), 'utf8')
const clientSource = await readFile(new URL('../api/client.ts', import.meta.url), 'utf8')

test('purchases live in the statement tabs, not as a separate dashboard section', () => {
  assert.equal(/<PurchaseHistory\s*\/>/.test(dashboardSource), false)
  assert.match(tabsSource, /<PurchasesPanel\s*\/>/)
})

test('sandbox credits and digital products stay separate lists with their own queries', () => {
  assert.match(panelSource, /queryKey: \['purchases', 'sandbox'\]/)
  assert.match(panelSource, /queryKey: \['purchases', 'product'\]/)
  assert.match(panelSource, /purchases\.kind\./)
  assert.match(panelSource, /useInfiniteSentinel/)
})

test('both purchase lists use ownership-scoped user routes with pagination', () => {
  assert.match(clientSource, /SANDBOX_PURCHASES_PATH/)
  assert.match(clientSource, /PRODUCT_PURCHASES_PATH/)
  assert.match(panelSource, /fetchNextPage/)
})
```

`ui-adaptation.test.mjs` line 32 asserts `tabIndex={selectedTab === type ? 0 : -1}`; keep the JSX variable named `type` in `ledger-tabs.tsx` so that pattern still matches (see Step 3).

- [ ] **Step 2: Run to verify they fail**

Run: `cd ui && npm test 2>&1 | tail -30` -> FAIL (new assertions, missing `purchases-panel.tsx`).

- [ ] **Step 3: Generalize the tab helpers and component**

`ledger-tabs.ts`:

```ts
export const LEDGER_TAB_KEYS = ['ArrowLeft', 'ArrowRight', 'Home', 'End'] as const

export type LedgerTabKey = typeof LEDGER_TAB_KEYS[number]

export function nextLedgerTab<T extends string>(
  tabs: T[],
  current: T,
  key: string,
): T | null {
  /* body unchanged */
}
```
(remove the `WalletType` import if now unused.)

`ledger-tabs.tsx`:

```tsx
import {PurchasesPanel} from '@/components/wallet/purchases-panel'

export const PURCHASES_TAB = 'purchases'
export type LedgerTab = WalletType | typeof PURCHASES_TAB

export function availableLedgerTabs(activated: boolean, hasSandbox: boolean): LedgerTab[] {
  const wallets: WalletType[] = activated ? ['real', 'game', 'sandbox'] : hasSandbox ? ['real', 'sandbox'] : ['real']
  return [...wallets, PURCHASES_TAB]
}
```

Hmm: the visibility test regex `hasSandbox \? \['real', 'sandbox'\] : \['real'\]` matches the ternary above (`hasSandbox ? ['real', 'sandbox'] : ['real']`), and `[...wallets, PURCHASES_TAB]` matches the second assertion. Keep both spellings exactly.

Replace every `WalletType` in the state, refs and helpers (`tabID`, `panelID`, `tabRefs`, `useState<WalletType>`, `handleKeyDown`) with `LedgerTab`. Keep the loop variable named `type`. Panel body:

```tsx
          {selectedTab === type && (type === PURCHASES_TAB ? <PurchasesPanel/> : <LedgerList type={type}/>)}
```

The read-only banner shrinks to the title only (the description restated what the tab name and the title already say):

```tsx
      {!activated && hasSandbox && selectedTab === 'sandbox' && (
        <div className="border-b border-border bg-muted/40 px-5 py-3">
          <p className="text-sm font-medium text-foreground">{t('dashboard.ledger.readOnly.title')}</p>
        </div>
      )}
```

Tab label: `t(`dashboard.ledger.tab.${type}`)` already works for the new key (Step 5).

- [ ] **Step 4: Build `purchases-panel.tsx`**

```tsx
'use client'

import {useState} from 'react'
import {useInfiniteQuery} from '@tanstack/react-query'
import {useTranslation} from 'react-i18next'
import {Button} from '@/components/ui/button'
import {QueryErrorState} from '@/components/query-error-state'
import {apiClient} from '@/lib/api/client'
import {useInfiniteSentinel} from '@/lib/hooks/useInfiniteSentinel'
import type {ProductPurchase, PurchasePage, PurchaseStatus, SandboxPurchase} from '@/lib/types/api'
import {formatBRL, formatCreditsAmount} from '@/lib/utils/money'

type PurchaseKind = 'sandbox' | 'product'
type PurchaseRecord = SandboxPurchase | ProductPurchase

const KINDS: PurchaseKind[] = ['sandbox', 'product']

const STATUS_STYLE: Record<PurchaseStatus, string> = {
  pending: 'bg-gray-100 text-gray-700',
  confirmed: 'bg-brand-50 text-brand-700',
  refund_pending: 'bg-gray-100 text-gray-700',
  refunded: 'bg-gray-100 text-gray-700',
  expired: 'bg-gray-100 text-gray-700',
}

const FETCHERS: Record<PurchaseKind, (cursor?: string) => Promise<PurchasePage<PurchaseRecord>>> = {
  sandbox: (cursor) => apiClient.getSandboxPurchases(cursor),
  product: (cursor) => apiClient.getProductPurchases(cursor),
}

function readableSKU(sku: string): string {
  return sku.replaceAll('_', ' ')
}

function PurchaseList({kind}: { kind: PurchaseKind }) {
  const {t, i18n} = useTranslation()
  const query = useInfiniteQuery({
    queryKey: ['purchases', kind],
    queryFn: ({pageParam}) => FETCHERS[kind](pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => (page.has_next && page.next_cursor ? page.next_cursor : undefined),
    refetchOnWindowFocus: false,
  })
  const items = query.data?.pages.flatMap((page) => page.items) ?? []
  const sentinelRef = useInfiniteSentinel({
    hasNextPage: !!query.hasNextPage,
    isFetching: query.isFetching,
    hasError: query.isFetchNextPageError,
    itemCount: items.length,
    fetchNextPage: () => void query.fetchNextPage(),
  })
  const dateFmt = new Intl.DateTimeFormat(i18n.language || 'pt-BR', {
    day: '2-digit', month: 'short', year: 'numeric', hour: '2-digit', minute: '2-digit',
  })

  if (query.isLoading) {
    return (
      <div role="status" className="space-y-3 px-5 py-5" aria-label={t('purchases.loading')}>
        <div className="h-14 animate-pulse rounded-lg bg-muted motion-reduce:animate-none"/>
      </div>
    )
  }
  if (query.error && !query.data) {
    return (
      <QueryErrorState
        message={t('purchases.error')}
        retrying={query.isFetching}
        onRetry={() => void query.refetch()}
        className="rounded-none border-0 px-5 py-6 text-center"
      />
    )
  }
  if (items.length === 0) {
    return <p className="px-5 py-8 text-center text-sm text-muted-foreground">{t(`purchases.empty.${kind}`)}</p>
  }

  return (
    <>
      <ul className="divide-y divide-border">
        {items.map((purchase) => (
          <li key={purchase.purchase_id} className="flex items-start justify-between gap-4 px-5 py-4">
            <div className="min-w-0">
              <div className="flex flex-wrap items-center gap-2">
                <p className="text-sm font-medium text-foreground">
                  {t(`purchases.sku.${purchase.sku}`, {defaultValue: readableSKU(purchase.sku)})}
                </p>
                <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${STATUS_STYLE[purchase.status]}`}>
                  {t(`purchases.status.${purchase.status}`)}
                </span>
              </div>
              {purchase.description && (
                <p className="mt-1 line-clamp-2 text-sm text-foreground">{purchase.description}</p>
              )}
              <p className="mt-1 text-xs text-muted-foreground">{dateFmt.format(new Date(purchase.created_at))}</p>
            </div>
            <div className="shrink-0 text-right">
              <p className="font-mono text-sm font-medium tabular-nums text-foreground">
                {formatBRL(purchase.amount_expected)}
              </p>
              {'credits_granted' in purchase && (
                <p className="mt-0.5 font-mono text-xs tabular-nums text-muted-foreground">
                  {t('purchases.credits', {credits: formatCreditsAmount(purchase.credits_granted)})}
                </p>
              )}
            </div>
          </li>
        ))}
      </ul>

      {query.isFetchNextPageError && (
        <div className="border-t border-border p-4 text-center">
          <p role="alert" className="mb-3 text-sm text-destructive">{t('purchases.loadMoreError')}</p>
          <Button variant="outline" onClick={() => void query.fetchNextPage()} disabled={query.isFetchingNextPage}>
            {t('common.retry')}
          </Button>
        </div>
      )}
      {query.hasNextPage && !query.isFetchNextPageError && (
        <div ref={sentinelRef} className="p-4 text-center" aria-hidden={!query.isFetchingNextPage}>
          {query.isFetchingNextPage && (
            <span role="status" className="text-sm text-muted-foreground">{t('purchases.loadingMore')}</span>
          )}
        </div>
      )}
    </>
  )
}

/** Purchases tab: sandbox credits and digital products are different objects, so they are two filters, not one merged list. */
export function PurchasesPanel() {
  const {t} = useTranslation()
  const [kind, setKind] = useState<PurchaseKind>('sandbox')
  return (
    <div>
      <div className="flex gap-2 border-b border-border px-5 py-3" role="group" aria-label={t('purchases.filter')}>
        {KINDS.map((k) => (
          <Button
            key={k}
            type="button"
            size="sm"
            variant={kind === k ? 'brand' : 'outline'}
            aria-pressed={kind === k}
            onClick={() => setKind(k)}
          >
            {t(`purchases.kind.${k}`)}
          </Button>
        ))}
      </div>
      <PurchaseList key={kind} kind={kind}/>
    </div>
  )
}
```

Check `Button` supports `size="sm"` and `variant="brand" | "outline"` (`rg "variant|size" ui/src/components/ui/button.tsx`); use the sizes that exist. `purchase.status` rows: the `'credits_granted' in purchase` narrowing replaces the old `kind` tagging.

Then: `git rm ui/src/components/wallet/purchase-history.tsx`; in `dashboard/page.tsx` delete the `PurchaseHistory` import and the `<PurchaseHistory/>` element.

- [ ] **Step 5: Locales (both files)**

Add: `dashboard.ledger.tab.purchases` ("Compras" / "Purchases"), `purchases.filter` ("Tipo de compra" / "Purchase type"), `purchases.loadMoreError` ("Não foi possível carregar mais compras" / "Couldn't load more purchases"). Shorten the existing tab labels so four fit on a phone: `dashboard.ledger.tab.real` "Real", `.game` "Jogo" / "Game", `.sandbox` "Sandbox". Remove now-unused: `purchases.title`, `purchases.description`, `purchases.loadMore`, `dashboard.ledger.readOnly.description`. Set `dashboard.ledger.readOnly.title` to "Somente consulta" / "View only". `dashboard.ledger.label` stays (used as the tablist `aria-label`; rename the value to "Extratos" / "Statements").

Verify no key is referenced but missing: `rg -o "t\\(['\"\`][a-zA-Z.\$\\{\\}]+" ui/src | sort -u` and compare against the JSON, or run the app and watch for raw keys.

- [ ] **Step 6: Verify and commit**

Run: `cd ui && npx eslint src --ext .ts,.tsx && npx tsc --noEmit && npm test 2>&1 | tail -30` -> clean, PASS.

Manual (mock mode): four tabs on a 360px viewport do not overflow badly (the tablist scrolls horizontally by design); arrow keys cycle through Compras; the two filters each show their own list and empty state.

```bash
git add -A ui && git commit -m "feat(ui): move purchase history into a Compras tab with infinite lists"
```

---

### Task 4: Remove every em dash from user-facing copy

**Files:**
- Modify: `ui/src/locales/pt-BR.json`, `ui/src/locales/en.json`
- Create: `ui/src/locales/no-em-dash.test.mjs`
- Modify: any `.tsx` with an em dash inside a rendered string (find with the scan below)

- [ ] **Step 1: Write the failing guard**

`ui/src/locales/no-em-dash.test.mjs`:

```js
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import test from 'node:test'

const EM_DASH = '—'

function collect(value, path, out) {
  if (typeof value === 'string') {
    if (value.includes(EM_DASH)) out.push(path)
  } else if (value && typeof value === 'object') {
    for (const [k, v] of Object.entries(value)) collect(v, path ? `${path}.${k}` : k, out)
  }
  return out
}

for (const file of ['pt-BR', 'en']) {
  test(`${file} locale has no em dash`, async () => {
    const json = JSON.parse(await readFile(new URL(`./${file}.json`, import.meta.url), 'utf8'))
    assert.deepEqual(collect(json, '', []), [])
  })
}

test('rendered JSX strings and string literals contain no em dash', async () => {
  const {readdir} = await import('node:fs/promises')
  const root = new URL('../', import.meta.url)
  const offenders = []
  async function walk(dir) {
    for (const entry of await readdir(dir, {withFileTypes: true})) {
      const url = new URL(entry.name + (entry.isDirectory() ? '/' : ''), dir)
      if (entry.isDirectory()) await walk(url)
      else if (/\.(tsx|ts)$/.test(entry.name) && !/\.test\./.test(entry.name)) {
        const lines = (await readFile(url, 'utf8')).split('\n')
        lines.forEach((line, i) => {
          if (!line.includes(EM_DASH)) return
          const t = line.trim()
          // Code comments are out of scope: line, block, JSDoc and JSX comments.
          if (t.startsWith('//') || t.startsWith('*') || t.startsWith('/*') || t.startsWith('{/*')) return
          offenders.push(`${url.pathname.split('/src/')[1]}:${i + 1}`)
        })
      }
    }
  }
  await walk(root)
  assert.deepEqual(offenders, [])
})
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd ui && node --disable-warning=MODULE_TYPELESS_PACKAGE_JSON --test --experimental-test-isolation=none src/locales/no-em-dash.test.mjs`
Expected: FAIL, listing the six keys per locale (plus any restored strings).

- [ ] **Step 3: Rewrite the copy (short, no dash), by key path**

Use this script so line numbers do not matter; it replaces only the listed keys and fails loudly if a key is missing:

`ui/scripts/fix-em-dash.mjs` (throwaway: run once, do not commit):

```js
import {readFile, writeFile} from 'node:fs/promises'

const EDITS = {
  'pt-BR': {
    'balance.game.subtitle': 'Dinheiro real reservado para jogos. Devolva ao saldo real para sacar.',
    'dialog.returnGame.description': 'Sem taxa e sem limite.',
    'confirm.returnGame.description': 'Sem taxa e sem limite.',
    'toast.withdrawReversed': 'Não foi possível concluir o saque. O valor voltou para o seu saldo.',
    'gambling.description': 'Carteira separada para jogos. O dinheiro continua seu e volta ao saldo real quando quiser, sem taxa e sem limite.',
    'systemState.unavailable.detail': 'Verificando automaticamente. Não precisa recarregar.',
  },
  en: {
    'balance.game.subtitle': 'Real money set aside for games. Return it to your real balance to withdraw.',
    'dialog.returnGame.description': 'No fee, no limit.',
    'confirm.returnGame.description': 'No fee, no limit.',
    'toast.withdrawReversed': 'Withdrawal failed. The amount is back in your balance.',
    'gambling.description': 'A separate wallet for games. The money stays yours and returns to your real balance anytime, with no fee and no limit.',
    'systemState.unavailable.detail': 'Checking automatically. No need to reload.',
  },
}

for (const [locale, edits] of Object.entries(EDITS)) {
  const path = new URL(`../src/locales/${locale}.json`, import.meta.url)
  const json = JSON.parse(await readFile(path, 'utf8'))
  for (const [key, value] of Object.entries(edits)) {
    const parts = key.split('.')
    const leaf = parts.pop()
    const parent = parts.reduce((o, k) => o?.[k], json)
    if (!parent || typeof parent[leaf] !== 'string') throw new Error(`${locale}: missing key ${key}`)
    parent[leaf] = value
  }
  await writeFile(path, JSON.stringify(json, null, 2) + '\n')
}
```

Run: `cd ui && node scripts/fix-em-dash.mjs && rm scripts/fix-em-dash.mjs`. Check `git diff --stat ui/src/locales` shows ONLY value changes (the script preserves key order; if the file used 2-space indentation and a trailing newline originally, the diff is minimal; if the diff is noisy, the original formatting differed: restore with `git checkout ui/src/locales` and edit the six lines per file by hand instead).

If Plan 1 Task 10 already restored strings that contain "—" (the guard test lists them), add their keys to `EDITS` using the same rule: one short sentence, bullet "•" to separate two facts, ";" to pause.

- [ ] **Step 4: Fix any rendered string in components**

For each offender the guard reports in `.tsx`/`.ts`, replace the dash in the rendered text with "•" or ";" (or restructure so the dash is not needed). A placeholder for an empty value that renders "—" becomes "•" only if it reads as "none"; otherwise render nothing.

- [ ] **Step 5: Verify and commit**

Run: `cd ui && npm test 2>&1 | tail -20 && npx eslint src --ext .ts,.tsx && npx tsc --noEmit` -> PASS, clean.

```bash
git add -A ui && git commit -m "fix(ui): remove em dashes from user-facing copy"
```

---

### Task 5: Copy diet (less text, more self-explanatory UI)

**Files:**
- Modify: `ui/src/locales/pt-BR.json`, `ui/src/locales/en.json`, components that render a removed string
- Create (throwaway): `ui/scripts/long-strings.mjs`

This task is a judgment call by nature, so it ends with an owner review before commit.

- [ ] **Step 1: List the candidates**

`ui/scripts/long-strings.mjs`:

```js
import {readFile} from 'node:fs/promises'

const json = JSON.parse(await readFile(new URL('../src/locales/pt-BR.json', import.meta.url), 'utf8'))
const rows = []
;(function walk(o, p) {
  for (const [k, v] of Object.entries(o)) {
    const path = p ? `${p}.${k}` : k
    if (typeof v === 'string' && v.length > 70) rows.push([v.length, path, v])
    else if (v && typeof v === 'object') walk(v, path)
  }
})(json, '')
rows.sort((a, b) => b[0] - a[0]).forEach(([n, path, v]) => console.log(`${n}\t${path}\t${v}`))
```

Run: `cd ui && node scripts/long-strings.mjs`. Do not commit the script.

- [ ] **Step 2: Apply the rules to each listed string, in both locales**

Rules (in priority order):
1. Delete a sentence when the element next to it already says it (a button labelled "Depositar" does not need "Faça um depósito para começar").
2. A title plus a description where the description repeats the title: keep the title, remove the description key AND its JSX element.
3. Empty states: one short phrase naming what is empty ("Nenhuma movimentação"), no instructions, the action is the nearby button.
4. Legal and responsible-gambling text required by the gambling addendum, terms acceptance and limits flows is NOT shortened by this task; list those keys in the PR description instead, for the owner to decide.
5. Error messages keep what happened and what to do, in one sentence.

Concrete starting points already known (apply, then extend using the list from Step 1):

| Key (pt-BR / en) | New pt-BR | New en |
|---|---|---|
| `dashboard.ledger.empty.real` | Nenhuma movimentação | No transactions |
| `dashboard.ledger.empty.game` | Nenhuma movimentação | No transactions |
| `dashboard.ledger.empty.sandbox` | Nenhuma movimentação | No transactions |
| `dashboard.ledger.error` | Não foi possível carregar o extrato | Couldn't load the statement |
| `dashboard.ledger.loadMoreError` | Não foi possível carregar mais | Couldn't load more |
| `purchases.error` | Não foi possível carregar as compras | Couldn't load purchases |
| `purchases.empty.sandbox` | Nenhuma compra de créditos | No credit purchases |
| `purchases.empty.product` | Nenhuma compra de produto | No product purchases |

Apply them with a throwaway script of the same shape as `fix-em-dash.mjs` (key path to new value, throw on a missing key), run it, delete it.

For each deleted key, delete the component code that rendered it (`rg "<key>" ui/src`), then confirm no raw key appears in the UI.

- [ ] **Step 3: Owner review gate**

Print the diff for review before committing:

```bash
git diff -- ui/src/locales | head -200
```

Stop here and show the diff to the repository owner; apply their edits; only then commit. Do not mark this task done without that review.

- [ ] **Step 4: Verify and commit**

Run: `cd ui && npx eslint src --ext .ts,.tsx && npx tsc --noEmit && npm test 2>&1 | tail -20` -> clean, PASS. Manual pass in both languages through: dashboard, each tab, deposit and withdraw dialogs (if the rail plan landed), game funding, limits.

```bash
git add -A ui && git commit -m "refactor(ui): cut explanatory copy in favor of self-describing elements"
```

---

### Task 6: Docs and cross-project note

**Files:**
- Modify: `ui/CLAUDE.md`, `ui/README.md` (if they describe the statement or purchase layout), `ui/DESIGN.md` (if it documents the tab set)

- [ ] **Step 1:** Document: the four tabs (Real, Jogo, Sandbox, Compras), the infinite-scroll hook and its contract (`useInfiniteSentinel` + `shouldLoadMore`), the rule "no em dash in user-facing copy; use bullet or semicolon", and "prefer elements over paragraphs". Note for `ctech-ui`: the sentinel hook is a candidate for the shared design system; do not copy it into other repos, propose it upstream (`ctech-ui` repo, `git checkout main && git pull origin main` first) as a separate change.
- [ ] **Step 2: Cross-project review (state in the PR):** ui <-> api (`balance_before`, cursor contract), ctech-ui (no change; upstream proposal noted), ctech-poker (the pattern was borrowed from `hands/page.tsx`; no change there).
- [ ] **Step 3: Final verification and commit**

```bash
cd ui && npx eslint src --ext .ts,.tsx && npx tsc --noEmit && npm test && npm run build
git add -A && git commit -m "docs(ui): document the tabs, infinite scroll and copy rules"
```

Suggested PR title: `feat(ui): infinite statements, purchases tab and leaner copy`.
