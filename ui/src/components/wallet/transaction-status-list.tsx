'use client'

import {useEffect, useRef, useState} from 'react'
import {CircleCheck, Clock3, RotateCcw, TriangleAlert} from 'lucide-react'
import {useTranslation} from 'react-i18next'
import {Button} from '@/components/ui/button'
import {formatBRL} from '@/lib/utils/money'
import type {TrackedTransaction, TrackedTransactionStatus} from '@/lib/utils/transaction-status'

const STATUS_STYLE: Record<TrackedTransactionStatus, string> = {
    pending: 'bg-gray-100 text-gray-700',
    processing: 'bg-gray-100 text-gray-700',
    confirmed: 'bg-brand-50 text-brand-700',
    completed: 'bg-brand-50 text-brand-700',
    expired: 'bg-gray-100 text-gray-700',
    reversed: 'bg-gray-100 text-gray-700',
    refund_failed: 'bg-destructive/10 text-destructive',
}

function StatusIcon({status}: {status: TrackedTransactionStatus}) {
    if (status === 'confirmed' || status === 'completed') return <CircleCheck size={18}/>
    if (status === 'reversed') return <RotateCcw size={18}/>
    if (status === 'refund_failed') return <TriangleAlert size={18}/>
    return <Clock3 size={18}/>
}

export function TransactionStatusList({
    transactions,
    activeDepositId,
    onResumeDeposit,
}: {
    transactions: TrackedTransaction[]
    activeDepositId?: string
    onResumeDeposit?: (txid: string) => void
}) {
    const {t, i18n} = useTranslation()
    const previousStatuses = useRef(new Map<string, TrackedTransactionStatus>())
    const [announcement, setAnnouncement] = useState('')

    useEffect(() => {
        const changes = transactions.flatMap((item) => {
            const previous = previousStatuses.current.get(item.id)
            previousStatuses.current.set(item.id, item.status)
            if (!previous || previous === item.status) return []
            return [t('transactions.statusAnnouncement', {
                kind: t(`transactions.kind.${item.kind}`),
                status: t(`transactions.status.${item.status}`),
            })]
        })
        if (changes.length > 0) setAnnouncement(changes.join(' '))
    }, [t, transactions])

    if (transactions.length === 0) return null

    const dateFmt = new Intl.DateTimeFormat(i18n.language || 'pt-BR', {
        dateStyle: 'short',
        timeStyle: 'short',
    })

    return (
        <section aria-labelledby="transaction-status-heading" className="overflow-hidden rounded-xl border border-border bg-card">
            <p className="sr-only" role="status" aria-live="polite" aria-atomic="true">
                {announcement}
            </p>
            <div className="border-b border-border px-5 py-4">
                <h2 id="transaction-status-heading" className="font-semibold text-foreground">
                    {t('transactions.title')}
                </h2>
                <p className="mt-1 text-sm text-muted-foreground">{t('transactions.description')}</p>
            </div>
            <ul className="divide-y divide-border">
                {transactions.map((item) => {
                    const canResume = item.kind === 'deposit'
                        && item.status === 'pending'
                        && item.id === activeDepositId
                        && onResumeDeposit
                    return (
                        <li key={item.id} className="flex min-w-0 flex-col gap-3 px-5 py-4 sm:flex-row sm:items-start sm:justify-between">
                            <div className="flex min-w-0 gap-3">
                                <span className="mt-0.5 text-muted-foreground" aria-hidden="true">
                                    <StatusIcon status={item.status}/>
                                </span>
                                <div className="min-w-0">
                                    <div className="flex flex-wrap items-center gap-2">
                                        <p className="text-sm font-semibold text-foreground">
                                            {t(`transactions.kind.${item.kind}`)}
                                        </p>
                                        <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${STATUS_STYLE[item.status]}`}>
                                            {t(`transactions.status.${item.status}`)}
                                        </span>
                                    </div>
                                    <p className="mt-1 text-xs text-muted-foreground">
                                        {dateFmt.format(new Date(item.created_at))}
                                    </p>
                                    <p className="mt-1 break-all font-mono text-xs text-muted-foreground">
                                        {t('transactions.reference', {id: item.id})}
                                    </p>
                                    {(item.status === 'processing' || item.status === 'refund_failed') && (
                                        <p className="mt-2 max-w-[65ch] text-sm leading-relaxed text-muted-foreground">
                                            {t(`transactions.guidance.${item.status}`)}
                                        </p>
                                    )}
                                    {canResume && (
                                        <Button variant="outline" size="sm" className="mt-3" onClick={() => onResumeDeposit(item.id)}>
                                            {t('transactions.resumeDeposit')}
                                        </Button>
                                    )}
                                </div>
                            </div>
                            <div className="shrink-0 sm:text-right">
                                <p className="font-mono text-sm font-semibold tabular-nums text-foreground">
                                    {formatBRL(item.amount)}
                                </p>
                                {item.kind === 'withdrawal' && item.fee != null && (
                                    <p className="mt-1 text-xs text-muted-foreground">
                                        {t('transactions.fee', {fee: formatBRL(item.fee)})}
                                    </p>
                                )}
                            </div>
                        </li>
                    )
                })}
            </ul>
        </section>
    )
}
