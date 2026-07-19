'use client'

import Link from 'next/link'
import {useState} from 'react'
import {useMutation, useQuery, useQueryClient} from '@tanstack/react-query'
import {ArrowLeft, ShieldAlert} from 'lucide-react'
import {toast} from 'sonner'
import {useTranslation} from 'react-i18next'
import {ProtectedRoute} from '@/components/protected-route'
import {Button} from '@/components/ui/button'
import {apiClient, ApiError} from '@/lib/api/client'
import {formatBRL, formatCredits, MAX_AMOUNT_DIGITS} from '@/lib/utils/money'
import type {GameLimitsInput} from '@/lib/types/api'

const EMPTY_LIMITS: GameLimitsInput = {daily_limit: 0, weekly_limit: 0, monthly_limit: 0}

function errorMessage(error: unknown, fallback: string): string {
    return error instanceof ApiError ? error.detail : fallback
}

function LimitInput({id, label, value, onChange}: {
    id: string
    label: string
    value: number
    onChange: (value: number) => void
}) {
    return (
        <label className="block text-sm font-medium text-foreground" htmlFor={id}>
            {label}
            <div className="mt-1.5 flex items-center gap-2 rounded-lg border border-border px-3 focus-within:border-brand-500 focus-within:ring-3 focus-within:ring-brand-500/20">
                <span className="text-sm text-muted-foreground">R$</span>
                <input
                    id={id}
                    inputMode="decimal"
                    value={formatCredits(value)}
                    onChange={(event) => {
                        const digits = event.target.value.replace(/\D/g, '').slice(0, MAX_AMOUNT_DIGITS)
                        onChange(Number.parseInt(digits || '0', 10))
                    }}
                    className="h-10 w-full border-0 bg-transparent font-mono tabular-nums outline-none"
                />
            </div>
        </label>
    )
}

function ResponsibleGamblingInner() {
    const {t, i18n} = useTranslation()
    const qc = useQueryClient()
    const status = useQuery({queryKey: ['gambling-limits'], queryFn: () => apiClient.getGameLimits()})
    const [draftLimits, setDraftLimits] = useState<GameLimitsInput | null>(null)
    const [period, setPeriod] = useState<'30d' | '90d' | 'indefinite'>('30d')
    const [confirming, setConfirming] = useState(false)
    const [confirmation, setConfirmation] = useState('')

    const refresh = () => {
        void qc.invalidateQueries({queryKey: ['gambling-limits']})
    }
    const save = useMutation({
        mutationFn: (input: GameLimitsInput) => apiClient.setGameLimits(input),
        onSuccess: () => { setDraftLimits(null); refresh(); toast.success(t('responsible.limits.saved')) },
        onError: (error) => toast.error(errorMessage(error, t('common.genericError'))),
    })
    const cancelPending = useMutation({
        mutationFn: () => apiClient.cancelPendingGameLimits(),
        onSuccess: () => { refresh(); toast.success(t('responsible.limits.pendingCancelled')) },
        onError: (error) => toast.error(errorMessage(error, t('common.genericError'))),
    })
    const exclude = useMutation({
        mutationFn: () => apiClient.selfExclude(period),
        onSuccess: () => {
            setConfirming(false)
            setConfirmation('')
            refresh()
            toast.success(t('responsible.exclusion.success'))
        },
        onError: (error) => toast.error(errorMessage(error, t('common.genericError'))),
    })

    const current = status.data?.limits
    const limits = draftLimits ?? (current ? {
        daily_limit: current.daily,
        weekly_limit: current.weekly,
        monthly_limit: current.monthly,
    } : EMPTY_LIMITS)
    const coherent = limits.daily_limit > 0 && limits.daily_limit <= limits.weekly_limit && limits.weekly_limit <= limits.monthly_limit
    const hasIncrease = !!current && (limits.daily_limit > current.daily || limits.weekly_limit > current.weekly || limits.monthly_limit > current.monthly)
    const locale = i18n.language || 'pt-BR'
    const date = (value: string) => new Intl.DateTimeFormat(locale, {dateStyle: 'long', timeStyle: 'short'}).format(new Date(value))

    return (
        <div className="min-h-screen bg-background px-4 py-8">
            <main className="mx-auto max-w-3xl space-y-6">
                <Link href="/dashboard" className="inline-flex items-center gap-2 text-sm text-muted-foreground hover:text-foreground">
                    <ArrowLeft size={16}/>{t('responsible.back')}
                </Link>
                <div>
                    <h1 className="text-2xl font-semibold text-foreground">{t('responsible.title')}</h1>
                    <p className="mt-1 text-sm text-muted-foreground">{t('responsible.description')}</p>
                </div>

                {status.isLoading && <div className="h-64 animate-pulse rounded-2xl bg-muted"/>}
                {status.error && <p className="rounded-xl border border-border bg-card p-5 text-sm">{t('common.genericError')}</p>}
                {status.data && (
                    <>
                        {status.data.excluded && (
                            <section className="rounded-2xl border border-destructive/30 bg-destructive/5 p-5" role="status">
                                <div className="flex gap-3"><ShieldAlert className="text-destructive"/><div>
                                    <h2 className="font-semibold">{t('responsible.exclusion.activeTitle')}</h2>
                                    <p className="mt-1 text-sm text-muted-foreground">
                                        {status.data.excluded.until
                                            ? t('responsible.exclusion.until', {date: date(status.data.excluded.until)})
                                            : t('responsible.exclusion.indefinite')}
                                    </p>
                                </div></div>
                            </section>
                        )}

                        <section className="rounded-2xl border border-border bg-card p-6">
                            <h2 className="text-lg font-semibold">{t('responsible.limits.title')}</h2>
                            <p className="mt-1 text-sm text-muted-foreground">{t('responsible.limits.description')}</p>
                            {current && (
                                <div className="mt-5 grid gap-4 sm:grid-cols-3">
                                    {(['daily', 'weekly', 'monthly'] as const).map((window) => {
                                        const used = status.data.usage[window]
                                        const limit = current[window]
                                        return <div key={window}>
                                            <div className="flex justify-between text-xs"><span>{t(`responsible.limits.${window}`)}</span><span>{formatBRL(used)} / {formatBRL(limit)}</span></div>
                                            <div className="mt-2 h-2 overflow-hidden rounded-full bg-muted"><div className="h-full bg-brand-600" style={{width: `${Math.min(100, limit ? used / limit * 100 : 0)}%`}}/></div>
                                        </div>
                                    })}
                                </div>
                            )}
                            <div className="mt-6 grid gap-4 sm:grid-cols-3">
                                <LimitInput id="daily-limit" label={t('responsible.limits.daily')} value={limits.daily_limit} onChange={(value) => setDraftLimits({...limits, daily_limit: value})}/>
                                <LimitInput id="weekly-limit" label={t('responsible.limits.weekly')} value={limits.weekly_limit} onChange={(value) => setDraftLimits({...limits, weekly_limit: value})}/>
                                <LimitInput id="monthly-limit" label={t('responsible.limits.monthly')} value={limits.monthly_limit} onChange={(value) => setDraftLimits({...limits, monthly_limit: value})}/>
                            </div>
                            {!coherent && <p className="mt-3 text-sm text-destructive">{t('responsible.limits.coherence')}</p>}
                            {hasIncrease && <p className="mt-3 rounded-lg bg-muted p-3 text-sm text-muted-foreground">{t('responsible.limits.cooldown')}</p>}
                            {current?.pending && <div className="mt-4 rounded-xl border border-brand-200 bg-brand-50 p-4 text-sm">
                                <p>{t('responsible.limits.pending', {date: date(current.pending.applies_at)})}</p>
                                <Button className="mt-3" variant="outline" onClick={() => cancelPending.mutate()} disabled={cancelPending.isPending}>{t('responsible.limits.cancelPending')}</Button>
                            </div>}
                            <Button className="mt-5" variant="brand" onClick={() => save.mutate(limits)} disabled={!coherent || save.isPending}>{t('responsible.limits.save')}</Button>
                        </section>

                        <section className="rounded-2xl border border-destructive/30 bg-card p-6">
                            <h2 className="text-lg font-semibold">{t('responsible.exclusion.title')}</h2>
                            <p className="mt-1 text-sm text-muted-foreground">{t('responsible.exclusion.description')}</p>
                            {!status.data.excluded && <>
                                <div className="mt-5 flex flex-wrap gap-4">
                                    {(['30d', '90d', 'indefinite'] as const).map((value) => <label key={value} className="flex items-center gap-2 text-sm"><input type="radio" name="period" value={value} checked={period === value} onChange={() => setPeriod(value)}/>{t(`responsible.exclusion.period.${value}`)}</label>)}
                                </div>
                                {!confirming ? <Button className="mt-5" variant="destructive" onClick={() => setConfirming(true)}>{t('responsible.exclusion.action')}</Button> : <div className="mt-5 rounded-xl bg-destructive/5 p-4">
                                    <p className="text-sm font-medium">{t('responsible.exclusion.confirm')}</p>
                                    <input value={confirmation} onChange={(event) => setConfirmation(event.target.value)} className="mt-3 h-10 w-full rounded-lg border border-border bg-background px-3" placeholder="EXCLUIR"/>
                                    <div className="mt-3 flex gap-2"><Button variant="ghost" onClick={() => setConfirming(false)}>{t('common.cancel')}</Button><Button variant="destructive" disabled={confirmation !== 'EXCLUIR' || exclude.isPending} onClick={() => exclude.mutate()}>{t('responsible.exclusion.confirmAction')}</Button></div>
                                </div>}
                            </>}
                        </section>
                    </>
                )}
            </main>
        </div>
    )
}

export default function ResponsibleGamblingPage() {
    return <ProtectedRoute><ResponsibleGamblingInner/></ProtectedRoute>
}
