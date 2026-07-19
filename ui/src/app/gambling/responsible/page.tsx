'use client'

import Link from 'next/link'
import {useState} from 'react'
import {useMutation, useQuery, useQueryClient} from '@tanstack/react-query'
import {ArrowLeft, ExternalLink, Phone, ShieldAlert} from 'lucide-react'
import {toast} from 'sonner'
import {useTranslation} from 'react-i18next'
import {ProtectedRoute} from '@/components/protected-route'
import {Button} from '@/components/ui/button'
import {apiClient, ApiError} from '@/lib/api/client'
import {formatBRL, formatCredits, MAX_AMOUNT_DIGITS} from '@/lib/utils/money'
import {matchesConfirmationPhrase} from '@/lib/utils/confirmation'
import {CVV_PHONE_URL, CVV_URL, GAMBLERS_ANONYMOUS_URL} from '@/lib/legal'
import type {GameLimitsInput} from '@/lib/types/api'
import {LanguageSwitcher} from '@/components/language-switcher'
import {QueryErrorState} from '@/components/query-error-state'

const EMPTY_LIMITS: GameLimitsInput = {daily_limit: 0, weekly_limit: 0, monthly_limit: 0}
const CONFIRMATION_INPUT_ID = 'self-exclusion-confirmation'
const CONFIRMATION_HINT_ID = 'self-exclusion-confirmation-hint'
const CONFIRMATION_ERROR_ID = 'self-exclusion-confirmation-error'

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
      <div
        className="mt-1.5 flex items-center gap-2 rounded-lg border border-border px-3 focus-within:border-brand-500 focus-within:ring-3 focus-within:ring-brand-500/20">
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
    onSuccess: () => {
      setDraftLimits(null);
      refresh();
      toast.success(t('responsible.limits.saved'))
    },
    onError: (error) => toast.error(errorMessage(error, t('common.genericError'))),
  })
  const cancelPending = useMutation({
    mutationFn: () => apiClient.cancelPendingGameLimits(),
    onSuccess: () => {
      refresh();
      toast.success(t('responsible.limits.pendingCancelled'))
    },
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
    onError: () => toast.error(t('responsible.exclusion.error')),
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
  const confirmationPhrase = t('responsible.exclusion.confirmationPhrase')
  const confirmationMatches = matchesConfirmationPhrase(confirmation, confirmationPhrase, locale)
  const showConfirmationError = confirmation.length > 0 && !confirmationMatches
  const date = (value: string) => new Intl.DateTimeFormat(locale, {
    dateStyle: 'long',
    timeStyle: 'short'
  }).format(new Date(value))

  return (
    <div className="min-h-screen bg-background px-4 py-8">
      <main className="mx-auto max-w-3xl space-y-6">
        <div className="flex items-center justify-between gap-4">
          <Link href="/dashboard"
                className="inline-flex items-center gap-2 text-sm text-muted-foreground hover:text-foreground [@media(pointer:coarse)]:min-h-11 [@media(pointer:coarse)]:min-w-11">
            <ArrowLeft size={16}/>{t('responsible.back')}
          </Link>
          <LanguageSwitcher/>
        </div>
        <div>
          <h1 className="text-2xl font-semibold text-foreground">{t('responsible.title')}</h1>
          <p className="mt-1 text-sm text-muted-foreground">{t('responsible.description')}</p>
        </div>

        {status.isLoading && <div className="h-64 animate-pulse rounded-2xl bg-muted" role="status">
            <span className="sr-only">{t('common.loading')}</span>
        </div>}
        {status.error && (
          <QueryErrorState
            message={t('common.genericError')}
            retrying={status.isFetching}
            onRetry={() => void status.refetch()}
          />
        )}
        {status.data && (
          <>
            {status.data.excluded && (
              <section className="rounded-2xl border border-destructive/30 bg-destructive/5 p-5" role="status">
                <div className="flex gap-3"><ShieldAlert className="text-destructive"/>
                  <div>
                    <h2 className="font-semibold">{t('responsible.exclusion.activeTitle')}</h2>
                    <p className="mt-1 text-sm text-muted-foreground">
                      {status.data.excluded.until
                        ? t('responsible.exclusion.until', {date: date(status.data.excluded.until)})
                        : t('responsible.exclusion.indefinite')}
                    </p>
                  </div>
                </div>
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
                      <div className="flex justify-between text-xs">
                        <span>{t(`responsible.limits.${window}`)}</span><span>{formatBRL(used)} / {formatBRL(limit)}</span>
                      </div>
                      <div
                        className="mt-2 h-2 overflow-hidden rounded-full bg-muted"
                        role="progressbar"
                        aria-label={t(`responsible.limits.${window}`)}
                        aria-valuemin={0}
                        aria-valuemax={limit}
                        aria-valuenow={used}
                        aria-valuetext={`${formatBRL(used)} / ${formatBRL(limit)}`}
                      >
                        <div className="h-full bg-brand-600"
                             style={{width: `${Math.min(100, limit ? used / limit * 100 : 0)}%`}}/>
                      </div>
                    </div>
                  })}
                </div>
              )}
              <div className="mt-6 grid gap-4 sm:grid-cols-3">
                <LimitInput id="daily-limit" label={t('responsible.limits.daily')} value={limits.daily_limit}
                            onChange={(value) => setDraftLimits({...limits, daily_limit: value})}/>
                <LimitInput id="weekly-limit" label={t('responsible.limits.weekly')} value={limits.weekly_limit}
                            onChange={(value) => setDraftLimits({...limits, weekly_limit: value})}/>
                <LimitInput id="monthly-limit" label={t('responsible.limits.monthly')} value={limits.monthly_limit}
                            onChange={(value) => setDraftLimits({...limits, monthly_limit: value})}/>
              </div>
              {!coherent && <p className="mt-3 text-sm text-destructive">{t('responsible.limits.coherence')}</p>}
              {hasIncrease &&
                  <p className="mt-3 rounded-lg bg-muted p-3 text-sm text-muted-foreground">{t('responsible.limits.cooldown')}</p>}
              {current?.pending && <div className="mt-4 rounded-xl border border-brand-200 bg-brand-50 p-4 text-sm">
                  <p>{t('responsible.limits.pending', {date: date(current.pending.applies_at)})}</p>
                  <Button className="mt-3" variant="outline" onClick={() => cancelPending.mutate()}
                          disabled={cancelPending.isPending}>{t('responsible.limits.cancelPending')}</Button>
              </div>}
              <Button className="mt-5" variant="brand" onClick={() => save.mutate(limits)}
                      disabled={!coherent || save.isPending}>{t('responsible.limits.save')}</Button>
            </section>

            <section className="rounded-2xl border border-destructive/30 bg-card p-6">
              <h2 className="text-lg font-semibold">{t('responsible.exclusion.title')}</h2>
              <p className="mt-1 text-sm text-muted-foreground">{t('responsible.exclusion.description')}</p>
              {!status.data.excluded && <>
                  <fieldset className="mt-5">
                      <legend className="text-sm font-medium text-foreground">
                        {t('responsible.exclusion.periodLabel')}
                      </legend>
                      <div className="mt-2 flex flex-wrap gap-2">
                        {(['30d', '90d', 'indefinite'] as const).map((value) => (
                          <label
                            key={value}
                            className={`flex min-h-11 cursor-pointer items-center gap-2 rounded-lg border px-3 text-sm ${
                              period === value
                                ? 'border-destructive bg-destructive/5'
                                : 'border-border'
                            }`}
                          >
                            <input
                              type="radio"
                              name="period"
                              value={value}
                              checked={period === value}
                              onChange={() => setPeriod(value)}
                              className="size-4 accent-destructive"
                            />
                            {t(`responsible.exclusion.period.${value}`)}
                          </label>
                        ))}
                      </div>
                  </fieldset>
                {!confirming ? <Button className="mt-5" variant="destructive" onClick={() => {
                  exclude.reset();
                  setConfirming(true)
                }}>{t('responsible.exclusion.action')}</Button> : <div className="mt-5 rounded-xl bg-destructive/5 p-4">
                  <p className="text-sm font-medium">
                    {t('responsible.exclusion.confirm', {
                      period: t(`responsible.exclusion.period.${period}`),
                      phrase: confirmationPhrase,
                    })}
                  </p>
                  <label htmlFor={CONFIRMATION_INPUT_ID} className="mt-4 block text-sm font-medium text-foreground">
                    {t('responsible.exclusion.confirmationLabel')}
                  </label>
                  <p id={CONFIRMATION_HINT_ID} className="mt-1 text-xs text-muted-foreground">
                    {t('responsible.exclusion.confirmationHint', {phrase: confirmationPhrase})}
                  </p>
                  <input
                    id={CONFIRMATION_INPUT_ID}
                    value={confirmation}
                    onChange={(event) => setConfirmation(event.target.value)}
                    className="mt-2 h-11 w-full rounded-lg border border-border bg-background px-3 font-mono uppercase focus:border-destructive focus:ring-3 focus:ring-destructive/20 focus:outline-none"
                    autoComplete="off"
                    autoFocus
                    spellCheck={false}
                    maxLength={confirmationPhrase.length + 2}
                    disabled={exclude.isPending}
                    aria-invalid={showConfirmationError}
                    aria-errormessage={showConfirmationError ? CONFIRMATION_ERROR_ID : undefined}
                    aria-describedby={CONFIRMATION_HINT_ID}
                  />
                  <div aria-live="polite">
                    {showConfirmationError && (
                      <p id={CONFIRMATION_ERROR_ID} className="mt-2 text-sm text-destructive">
                        {t('responsible.exclusion.confirmationMismatch', {phrase: confirmationPhrase})}
                      </p>
                    )}
                  </div>
                  {exclude.isError && (
                    <p role="alert" className="mt-2 text-sm text-destructive">
                      {t('responsible.exclusion.error')}
                    </p>
                  )}
                  <div className="mt-4 flex flex-col-reverse gap-2 sm:flex-row">
                    <Button
                      variant="ghost"
                      disabled={exclude.isPending}
                      onClick={() => {
                        exclude.reset();
                        setConfirming(false);
                        setConfirmation('')
                      }}
                    >
                      {t('common.cancel')}
                    </Button>
                    <Button
                      variant="destructive"
                      disabled={!confirmationMatches || exclude.isPending}
                      onClick={() => exclude.mutate()}
                    >
                      {exclude.isPending
                        ? t('responsible.exclusion.confirming')
                        : t('responsible.exclusion.confirmAction')}
                    </Button>
                  </div>
                </div>}
              </>}

              <div className="mt-6 border-t border-border pt-5">
                <h3 className="text-sm font-semibold text-foreground">
                  {t('responsible.exclusion.helpTitle')}
                </h3>
                <p className="mt-1 max-w-2xl text-sm text-muted-foreground">
                  {t('responsible.exclusion.helpDescription')}
                </p>
                <div className="mt-3 flex flex-col items-start gap-2 text-sm">
                  <a href={CVV_PHONE_URL}
                     className="inline-flex min-h-11 items-center gap-2 font-medium text-foreground underline underline-offset-4">
                    <Phone size={16} aria-hidden="true"/>
                    {t('responsible.exclusion.cvvPhone')}
                  </a>
                  <a href={CVV_URL} target="_blank" rel="noreferrer"
                     className="inline-flex min-h-11 items-center gap-2 font-medium text-foreground underline underline-offset-4">
                    <ExternalLink size={16} aria-hidden="true"/>
                    {t('responsible.exclusion.cvvWebsite')}
                  </a>
                  <a href={GAMBLERS_ANONYMOUS_URL} target="_blank" rel="noreferrer"
                     className="inline-flex min-h-11 items-center gap-2 font-medium text-foreground underline underline-offset-4">
                    <ExternalLink size={16} aria-hidden="true"/>
                    {t('responsible.exclusion.gamblersAnonymous')}
                  </a>
                </div>
              </div>
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
