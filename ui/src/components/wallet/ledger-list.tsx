'use client'

import {useInfiniteQuery} from '@tanstack/react-query'
import {useTranslation} from 'react-i18next'
import {apiClient} from '@/lib/api/client'
import {formatSigned} from '@/lib/utils/money'
import type {WalletType} from '@/lib/types/api'
import {Button} from '@/components/ui/button'

export function LedgerList({type}: { type: WalletType }) {
    const {t, i18n} = useTranslation()
    const real = type === 'real'
    const {
        data,
        isLoading,
        error,
        hasNextPage,
        fetchNextPage,
        isFetchingNextPage,
        isFetchNextPageError,
    } = useInfiniteQuery({
        queryKey: ['ledger', type],
        queryFn: ({pageParam}) => apiClient.getLedger(type, pageParam),
        initialPageParam: undefined as string | undefined,
        getNextPageParam: (lastPage) => lastPage.has_next && lastPage.next_cursor
            ? lastPage.next_cursor
            : undefined,
    })

    const dateFmt = new Intl.DateTimeFormat(i18n.language || 'pt-BR', {
        day: '2-digit',
        month: 'short',
        hour: '2-digit',
        minute: '2-digit',
    })

    if (isLoading) {
        return <p className="px-5 py-8 text-center text-sm text-muted-foreground">{t('dashboard.ledger.loading')}</p>
    }

    if (error && !data) {
        return (
            <p className="px-5 py-8 text-center text-sm text-muted-foreground">
                {t('dashboard.ledger.error')}
            </p>
        )
    }

    const items = data?.pages.flatMap((page) => page.items) ?? []

    if (items.length === 0) {
        return (
            <p className="px-5 py-10 text-center text-sm text-muted-foreground">
                {real ? t('dashboard.ledger.emptyReal') : t('dashboard.ledger.emptyOther')}
            </p>
        )
    }

    return (
        <>
            <ul className="divide-y divide-border">
                {items.map((entry) => (
                    <li key={entry.entry_id} className="flex items-center justify-between gap-4 px-5 py-3.5">
                        <div className="min-w-0">
                            <p className="text-sm font-medium text-foreground">{t(`ledger.type.${entry.type}`, entry.type)}</p>
                            <p className="mt-0.5 text-xs text-muted-foreground">{dateFmt.format(new Date(entry.created_at))}</p>
                        </div>
                        <p
                            className={`shrink-0 font-mono text-sm tabular-nums ${
                                entry.amount < 0 ? 'text-muted-foreground' : 'text-brand-700'
                            }`}
                        >
                            {formatSigned(entry.amount, real)}
                        </p>
                    </li>
                ))}
            </ul>
            {hasNextPage && (
                <div className="border-t border-border p-4 text-center">
                    {isFetchNextPageError && (
                        <p role="alert" className="mb-3 text-sm text-destructive">
                            {t('dashboard.ledger.loadMoreError')}
                        </p>
                    )}
                    <Button
                        variant="outline"
                        onClick={() => void fetchNextPage()}
                        disabled={isFetchingNextPage}
                    >
                        {isFetchingNextPage ? t('dashboard.ledger.loadingMore') : t('dashboard.ledger.loadMore')}
                    </Button>
                </div>
            )}
        </>
    )
}
