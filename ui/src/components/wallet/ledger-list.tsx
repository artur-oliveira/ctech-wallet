'use client'

import {useInfiniteQuery} from '@tanstack/react-query'
import {useTranslation} from 'react-i18next'
import {apiClient} from '@/lib/api/client'
import {useInfiniteSentinel} from '@/lib/hooks/useInfiniteSentinel'
import {formatBalance, formatSigned} from '@/lib/utils/money'
import {walletHasMonetaryValue} from '@/lib/utils/wallet-semantics'
import type {WalletType} from '@/lib/types/api'
import {Button} from '@/components/ui/button'
import {QueryErrorState} from '@/components/query-error-state'

const SANDBOX_PURCHASE_REFERENCE_PREFIX = 'sbxp'

function ledgerType(type: WalletType, entryType: string, ref?: string): string {
  if (type === 'sandbox' && entryType === 'sandbox_credit' && ref?.startsWith(SANDBOX_PURCHASE_REFERENCE_PREFIX)) {
    return 'sandbox_direct_purchase'
  }
  return entryType
}

export function LedgerList({type}: { type: WalletType }) {
  const {t, i18n} = useTranslation()
  const monetary = walletHasMonetaryValue(type)
  const {
    data,
    isLoading,
    error,
    hasNextPage,
    fetchNextPage,
    isFetchingNextPage,
    isFetchNextPageError,
    refetch,
    isFetching,
  } = useInfiniteQuery({
    queryKey: ['ledger', type],
    queryFn: ({pageParam}) => apiClient.getLedger(type, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (lastPage) => lastPage.has_next && lastPage.next_cursor
      ? lastPage.next_cursor
      : undefined,
    // A focus refetch would re-request EVERY loaded page.
    refetchOnWindowFocus: false,
  })

  const items = data?.pages.flatMap((page) => page.items) ?? []
  // A hook: must run before any early return below.
  const sentinelRef = useInfiniteSentinel({
    hasNextPage: !!hasNextPage,
    isFetching,
    hasError: isFetchNextPageError,
    itemCount: items.length,
    fetchNextPage: () => void fetchNextPage(),
  })

  const dateFmt = new Intl.DateTimeFormat(i18n.language || 'pt-BR', {
    day: '2-digit',
    month: 'short',
    hour: '2-digit',
    minute: '2-digit',
  })

  if (isLoading) {
    return <p role="status"
              className="px-5 py-8 text-center text-sm text-muted-foreground">{t('dashboard.ledger.loading')}</p>
  }

  if (error && !data) {
    return (
      <QueryErrorState
        message={t('dashboard.ledger.error')}
        retrying={isFetching}
        onRetry={() => void refetch()}
        className="rounded-none border-0 px-5 py-8 text-center"
      />
    )
  }

  if (items.length === 0) {
    return (
      <p className="px-5 py-10 text-center text-sm text-muted-foreground">
        {t(`dashboard.ledger.empty.${type}`)}
      </p>
    )
  }

  return (
    <>
      <ul className="divide-y divide-border">
        {items.map((entry) => {
          const label = t(`ledger.type.${ledgerType(type, entry.type, entry.ref)}`, entry.type)
          const when = dateFmt.format(new Date(entry.created_at))
          return (
            <li key={entry.entry_id} className="flex items-start justify-between gap-4 px-5 py-3.5">
              <div className="min-w-0">
                {/* The service-supplied description is what the user recognises,
                    so it leads; the type label drops to the second line. */}
                <p className="line-clamp-2 text-sm font-medium text-foreground">{entry.description || label}</p>
                <p className="mt-0.5 text-xs text-muted-foreground">
                  {entry.description ? `${label} • ${when}` : when}
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
            {t('common.tryAgain')}
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
}
