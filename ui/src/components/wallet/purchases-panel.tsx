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
    // A focus refetch would re-request EVERY loaded page.
    refetchOnWindowFocus: false,
  })
  const items = query.data?.pages.flatMap((page) => page.items) ?? []
  // A hook: must run before any early return below.
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
            {t('common.tryAgain')}
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
