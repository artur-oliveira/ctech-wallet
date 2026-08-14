'use client'

import {useInfiniteQuery} from '@tanstack/react-query'
import {useTranslation} from 'react-i18next'
import {Button} from '@/components/ui/button'
import {QueryErrorState} from '@/components/query-error-state'
import {apiClient} from '@/lib/api/client'
import type {ProductPurchase, PurchaseStatus, SandboxPurchase} from '@/lib/types/api'
import {formatBRL, formatCreditsAmount} from '@/lib/utils/money'

type PurchaseRecord =
  | ({kind: 'sandbox'} & SandboxPurchase)
  | ({kind: 'product'} & ProductPurchase)

const STATUS_STYLE: Record<PurchaseStatus, string> = {
  pending: 'bg-gray-100 text-gray-700',
  confirmed: 'bg-brand-50 text-brand-700',
  refund_pending: 'bg-gray-100 text-gray-700',
  refunded: 'bg-gray-100 text-gray-700',
  expired: 'bg-gray-100 text-gray-700',
}

function readableSKU(sku: string): string {
  return sku.replaceAll('_', ' ')
}

interface PurchaseGroupProps {
  kind: 'sandbox' | 'product'
  items: PurchaseRecord[]
  loading: boolean
  error: Error | null
  fetching: boolean
  loadingMore: boolean
  hasMore: boolean
  onRetry: () => void
  onLoadMore: () => void
  dateFmt: Intl.DateTimeFormat
}

function PurchaseGroup({
                         kind,
                         items,
                         loading,
                         error,
                         fetching,
                         loadingMore,
                         hasMore,
                         onRetry,
                         onLoadMore,
                         dateFmt,
                       }: PurchaseGroupProps) {
  const {t} = useTranslation()
  const headingID = `purchase-history-${kind}`

  return (
    <section aria-labelledby={headingID} className="border-b border-border last:border-b-0">
      <div className="bg-muted/40 px-5 py-3">
        <h3 id={headingID} className="text-sm font-medium text-foreground">
          {t(`purchases.kind.${kind}`)}
        </h3>
      </div>

      {loading && (
        <div role="status" className="space-y-3 px-5 py-5" aria-label={t('purchases.loading')}>
          <div className="h-14 animate-pulse rounded-lg bg-muted motion-reduce:animate-none"/>
        </div>
      )}

      {error && (
        <QueryErrorState
          message={t('purchases.error')}
          retrying={fetching}
          onRetry={onRetry}
          className="rounded-none border-0 px-5 py-6 text-center"
        />
      )}

      {!loading && !error && items.length === 0 && (
        <p className="px-5 py-8 text-center text-sm text-muted-foreground">
          {t(`purchases.empty.${kind}`)}
        </p>
      )}

      {items.length > 0 && (
        <ul className="divide-y divide-border">
          {items.map((purchase) => (
            <li key={purchase.purchase_id} className="flex flex-col gap-3 px-5 py-4 sm:flex-row sm:items-center sm:justify-between">
              <div className="min-w-0">
                <div className="flex flex-wrap items-center gap-2">
                  <p className="text-sm font-medium text-foreground">
                    {t(`purchases.sku.${purchase.sku}`, {defaultValue: readableSKU(purchase.sku)})}
                  </p>
                  <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${STATUS_STYLE[purchase.status]}`}>
                    {t(`purchases.status.${purchase.status}`)}
                  </span>
                </div>
                <p className="mt-1 text-xs text-muted-foreground">
                  {dateFmt.format(new Date(purchase.created_at))}
                </p>
              </div>
              <div className="shrink-0 sm:text-right">
                <p className="font-mono text-sm font-medium tabular-nums text-foreground">
                  {formatBRL(purchase.amount_expected)}
                </p>
                {purchase.kind === 'sandbox' && (
                  <p className="mt-0.5 font-mono text-xs tabular-nums text-muted-foreground">
                    {t('purchases.credits', {credits: formatCreditsAmount(purchase.credits_granted)})}
                  </p>
                )}
              </div>
            </li>
          ))}
        </ul>
      )}

      {hasMore && (
        <div className="border-t border-border p-4 text-center">
          <Button variant="outline" onClick={onLoadMore} disabled={loadingMore}>
            {loadingMore ? t('purchases.loadingMore') : t('purchases.loadMore')}
          </Button>
        </div>
      )}
    </section>
  )
}

export function PurchaseHistory() {
  const {t, i18n} = useTranslation()
  const sandbox = useInfiniteQuery({
    queryKey: ['purchases', 'sandbox'],
    queryFn: ({pageParam}) => apiClient.getSandboxPurchases(pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => page.has_next && page.next_cursor ? page.next_cursor : undefined,
  })
  const products = useInfiniteQuery({
    queryKey: ['purchases', 'product'],
    queryFn: ({pageParam}) => apiClient.getProductPurchases(pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => page.has_next && page.next_cursor ? page.next_cursor : undefined,
  })

  const sandboxItems: PurchaseRecord[] = sandbox.data?.pages.flatMap(
    (page) => page.items.map((item) => ({...item, kind: 'sandbox' as const})),
  ) ?? []
  const productItems: PurchaseRecord[] = products.data?.pages.flatMap(
    (page) => page.items.map((item) => ({...item, kind: 'product' as const})),
  ) ?? []

  const dateFmt = new Intl.DateTimeFormat(i18n.language || 'pt-BR', {
    day: '2-digit', month: 'short', year: 'numeric', hour: '2-digit', minute: '2-digit',
  })

  return (
    <section aria-labelledby="purchase-history-heading" className="overflow-hidden rounded-xl border border-border bg-card">
      <div className="border-b border-border px-5 py-4">
        <h2 id="purchase-history-heading" className="font-semibold text-foreground">
          {t('purchases.title')}
        </h2>
        <p className="mt-1 max-w-[70ch] text-sm leading-relaxed text-muted-foreground">
          {t('purchases.description')}
        </p>
      </div>

      <PurchaseGroup
        kind="sandbox"
        items={sandboxItems}
        loading={sandbox.isLoading}
        error={sandbox.error}
        fetching={sandbox.isFetching}
        loadingMore={sandbox.isFetchingNextPage}
        hasMore={!!sandbox.hasNextPage}
        onRetry={() => void sandbox.refetch()}
        onLoadMore={() => void sandbox.fetchNextPage()}
        dateFmt={dateFmt}
      />
      <PurchaseGroup
        kind="product"
        items={productItems}
        loading={products.isLoading}
        error={products.error}
        fetching={products.isFetching}
        loadingMore={products.isFetchingNextPage}
        hasMore={!!products.hasNextPage}
        onRetry={() => void products.refetch()}
        onLoadMore={() => void products.fetchNextPage()}
        dateFmt={dateFmt}
      />
    </section>
  )
}
