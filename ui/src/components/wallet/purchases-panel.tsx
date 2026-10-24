'use client'

import {useState} from 'react'
import {useInfiniteQuery} from '@tanstack/react-query'
import {useTranslation} from 'react-i18next'
import {Badge, Button, type BadgeTone, EmptyState, Segmented, Skeleton} from '@aoctech/ui'
import {QueryErrorState} from '@/components/query-error-state'
import {apiClient} from '@/lib/api/client'
import {useInfiniteSentinel} from '@/lib/hooks/useInfiniteSentinel'
import type {ProductPurchase, PurchasePage, PurchaseStatus, SandboxPurchase} from '@/lib/types/api'
import {formatBRL, formatCreditsAmount} from '@/lib/utils/money'

type PurchaseKind = 'sandbox' | 'product'
type PurchaseRecord = SandboxPurchase | ProductPurchase

const KINDS: PurchaseKind[] = ['sandbox', 'product']

const STATUS_TONE: Record<PurchaseStatus, BadgeTone> = {
  pending: 'attention',
  confirmed: 'positive',
  refund_pending: 'attention',
  refunded: 'neutral',
  expired: 'neutral',
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
    getNextPageParam: (page) => page.next_cursor ?? undefined,
    // A focus refetch would re-request EVERY loaded page.
    refetchOnWindowFocus: false,
  })
  const items = query.data?.pages.flatMap((page) => page.data) ?? []
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
        <Skeleton className="h-14 rounded-lg"/>
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
    return <EmptyState title={t(`purchases.empty.${kind}`)} className="rounded-none border-0 py-8"/>
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
                <Badge tone={STATUS_TONE[purchase.status]}>
                  {t(`purchases.status.${purchase.status}`)}
                </Badge>
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
      <div className="border-b border-border px-5 py-3">
        <Segmented
          label={t('purchases.filter')}
          value={kind}
          onValueChange={setKind}
          options={KINDS.map((k) => ({value: k, label: t(`purchases.kind.${k}`)}))}
        />
      </div>
      <PurchaseList key={kind} kind={kind}/>
    </div>
  )
}
