'use client'

import {useState} from 'react'
import dynamic from 'next/dynamic'
import {useMutation, useQuery, useQueryClient} from '@tanstack/react-query'
import {toast} from 'sonner'
import {LogOut} from 'lucide-react'
import {useTranslation} from 'react-i18next'
import {apiClient, ApiError} from '@/lib/api/client'
import {formatBRL, formatCreditsAmount, toCredits} from '@/lib/utils/money'
import {useAuth} from '@/lib/hooks/useAuth'
import {ProtectedRoute} from '@/components/protected-route'
import {BalanceCards} from '@/components/wallet/balance-cards'
import {LedgerTabs} from '@/components/wallet/ledger-tabs'
import {PurchaseHistory} from '@/components/wallet/purchase-history'
import {Button} from '@/components/ui/button'
import {QueryErrorState} from '@/components/query-error-state'
import {LanguageSwitcher} from '@/components/language-switcher'
import {useWalletRealtime} from '@/lib/hooks/useWalletRealtime'
import Image from 'next/image'

type Flow = 'credits' | 'fund-game' | 'return-game' | null

const AmountDialog = dynamic(() => import('@/components/wallet/amount-dialog').then((module) => module.AmountDialog))
const ConfirmMoneyDialog = dynamic(() => import('@/components/wallet/confirm-money-dialog').then((module) => module.ConfirmMoneyDialog))
const MoneyReceiptDialog = dynamic(() => import('@/components/wallet/money-receipt-dialog').then((module) => module.MoneyReceiptDialog))

/** RFC 7807 problem type → i18n key. */
const PROBLEM_KEY: Record<string, string> = {
  '/problems/insufficient-balance': 'errors.insufficientBalance',
  '/problems/wallet-busy': 'errors.walletBusy',
  '/problems/kyc-not-verified': 'errors.kycNotVerified',
  '/problems/idempotency-conflict': 'errors.idempotencyConflict',
  '/problems/gambling-not-activated': 'errors.gamblingNotActivated',
  '/problems/gambling-terms-required': 'errors.gamblingTermsRequired',
  '/problems/amount-above-limit': 'errors.amountAboveLimit',
  '/problems/self-excluded': 'errors.selfExcluded',
  '/problems/limits-not-configured': 'errors.limitsNotConfigured',
  '/problems/deposit-limit-exceeded': 'errors.depositLimitExceeded',
  '/problems/account-blocked': 'errors.accountBlocked',
}

/** Turns an RFC 7807 problem from the API into copy the user can act on. */
function problemMessage(err: unknown, t: (k: string, o?: Record<string, unknown>) => string): string {
  if (!(err instanceof ApiError)) return t('common.genericError')
  if (err.type === '/problems/amount-above-limit') {
    const {max_amount: max} = (err.raw ?? {}) as { max_amount?: number }
    if (max == null) return err.detail || t('errors.generic')
    return t('errors.amountAboveLimit', {max: formatBRL(max)})
  }
  const key = err.type ? PROBLEM_KEY[err.type] : undefined
  if (!key) return err.detail || t('errors.generic')
  return t(key)
}

/** A fresh idempotency key per submit attempt — replays are safe server-side. */
function newIdemKey(): string {
  return crypto.randomUUID()
}

function DashboardInner() {
  const {t} = useTranslation()
  const {profile, logout} = useAuth()
  const qc = useQueryClient()
  const [flow, setFlow] = useState<Flow>(null)
  const [confirm, setConfirm] = useState<{
    flow: 'fund-game' | 'return-game';
    amount: number
  } | null>(null)
  const [receipt, setReceipt] = useState<{
    title: string
    amountLabel: string
    details?: Array<{ label: string; value: string }>
  } | null>(null)
  const balances = useQuery({queryKey: ['balances'], queryFn: () => apiClient.getBalances()})
  const responsible = useQuery({queryKey: ['gambling-limits'], queryFn: () => apiClient.getGameLimits()})

  const {wsStatus} = useWalletRealtime()

  function refresh() {
    void qc.invalidateQueries({queryKey: ['balances']})
    void qc.invalidateQueries({queryKey: ['ledger']})
  }

  const buyCredits = useMutation({
    mutationFn: (amount: number) => apiClient.purchaseSandbox(amount, newIdemKey()),
    onSuccess: (_transfer, amount) => {
      setFlow(null)
      refresh()
      setReceipt({title: t('toast.creditsAdded'), amountLabel: formatCreditsAmount(toCredits(amount))})
    },
    onError: (err) => toast.error(problemMessage(err, t)),
  })

  const fundGame = useMutation({
    mutationFn: (amount: number) => apiClient.fundGame(amount, newIdemKey()),
    onSuccess: (_transfer, amount) => {
      setConfirm(null)
      setFlow(null)
      refresh()
      setReceipt({title: t('toast.fundGameSent'), amountLabel: formatBRL(amount)})
    },
    onError: (err) => toast.error(problemMessage(err, t)),
  })

  const returnFromGame = useMutation({
    mutationFn: (amount: number) => apiClient.returnFromGame(amount, newIdemKey()),
    onSuccess: (_transfer, amount) => {
      setConfirm(null)
      setFlow(null)
      refresh()
      setReceipt({title: t('toast.returned'), amountLabel: formatBRL(amount)})
    },
    onError: (err) => toast.error(problemMessage(err, t)),
  })

  const name = profile?.first_name ?? profile?.username ?? ''

  return (
    <div className="min-h-screen bg-background">
      <header className="border-b border-border bg-card">
        <h1 className="sr-only">CTech Ledger</h1>
        <div className="mx-auto flex max-w-4xl items-center justify-between gap-3 px-4 py-4 sm:px-6">
          <div className="flex items-center gap-2.5">
            <div className="flex size-8 items-center justify-center rounded-lg bg-brand-600 text-white">
              <Image src="/app.svg"
                     alt=""
                     width={32}
                     height={32}/>
            </div>
            <span className="font-semibold text-foreground">CTech Ledger</span>
          </div>
          <div className="flex min-w-0 items-center gap-3">
                        <span
                          className="inline-flex items-center gap-1.5"
                          role="status"
                          aria-label={
                            wsStatus === 'connected'
                              ? t('dashboard.live')
                              : wsStatus === 'connecting'
                                ? t('dashboard.connecting')
                                : t('dashboard.offline')
                          }
                        >
                            <span
                              className={`size-1.5 rounded-full ${
                                wsStatus === 'connected'
                                  ? 'bg-brand-600'
                                  : wsStatus === 'connecting'
                                    ? 'bg-brand-300'
                                    : 'bg-gray-300'
                              }`}
                            />
                            <span className="hidden text-xs text-muted-foreground sm:inline">
                                {wsStatus === 'connected'
                                  ? t('dashboard.live')
                                  : wsStatus === 'connecting'
                                    ? t('dashboard.connecting')
                                    : t('dashboard.offline')}
                            </span>
                        </span>
            {name &&
                <span className="hidden max-w-[10rem] truncate text-sm text-muted-foreground lg:inline">{name}</span>}
            <LanguageSwitcher/>
            <Button variant="ghost" size="icon-sm" onClick={logout} aria-label={t('dashboard.logout')}>
              <LogOut size={16}/>
            </Button>
          </div>
        </div>
      </header>

      <main className="mx-auto max-w-4xl space-y-6 px-4 py-8 sm:px-6">
        {balances.isLoading && (
          <div className="h-44 animate-pulse rounded-2xl bg-muted" role="status">
            <span className="sr-only">{t('dashboard.loadingBalances')}</span>
          </div>
        )}

        {balances.error && (
          <QueryErrorState
            message={t('dashboard.loadError')}
            retrying={balances.isFetching}
            onRetry={() => void balances.refetch()}
          />
        )}

        {balances.data && (
          <>
            <BalanceCards
              balances={balances.data}
              onBuyCredits={() => setFlow('credits')}
              onFundGame={() => setFlow('fund-game')}
              onReturnFromGame={() => setFlow('return-game')}
              selfExcluded={!!responsible.data?.excluded}
            />

            {responsible.data?.excluded && (
              <section className="rounded-xl border border-destructive/30 bg-destructive/5 p-4 text-sm">
                <p className="font-semibold">{t('responsible.exclusion.activeTitle')}</p>
                <p className="mt-1 text-muted-foreground">{responsible.data.excluded.until
                  ? t('responsible.exclusion.until', {date: new Intl.DateTimeFormat(undefined, {dateStyle: 'long'}).format(new Date(responsible.data.excluded.until))})
                  : t('responsible.exclusion.indefinite')}</p>
              </section>
            )}

            <LedgerTabs
              activated={balances.data.activated}
              hasSandbox={!!balances.data.sandbox}
            />

            <PurchaseHistory/>
          </>
        )}
      </main>

      {flow === 'credits' && (
        <AmountDialog
          flow="credits"
          maxCents={balances.data?.game?.balance}
          pending={buyCredits.isPending}
          onSubmit={(amount) => buyCredits.mutate(amount)}
          onClose={() => setFlow(null)}
        />
      )}

      {flow === 'fund-game' && (
        <AmountDialog
          flow="fund-game"
          maxCents={balances.data?.real?.balance}
          pending={fundGame.isPending || confirm?.flow === 'fund-game'}
          onProceed={(amount) => setConfirm({flow: 'fund-game', amount})}
          onClose={() => setFlow(null)}
        />
      )}

      {flow === 'return-game' && (
        <AmountDialog
          flow="return-game"
          maxCents={balances.data?.game?.balance}
          pending={returnFromGame.isPending || confirm?.flow === 'return-game'}
          onProceed={(amount) => setConfirm({flow: 'return-game', amount})}
          onClose={() => setFlow(null)}
        />
      )}

      {confirm && (
        <ConfirmMoneyDialog
          flow={confirm.flow}
          amountCents={confirm.amount}
          availableCents={
            confirm.flow === 'return-game'
              ? balances.data?.game?.balance ?? 0
              : balances.data?.real?.balance ?? 0
          }
          pending={confirm.flow === 'fund-game' ? fundGame.isPending : returnFromGame.isPending}
          onConfirm={() => {
            if (confirm.flow === 'fund-game') {
              fundGame.mutate(confirm.amount)
            } else {
              returnFromGame.mutate(confirm.amount)
            }
          }}
          onClose={() => setConfirm(null)}
        />
      )}

      {receipt && (
        <MoneyReceiptDialog
          title={receipt.title}
          amountLabel={receipt.amountLabel}
          details={receipt.details}
          onClose={() => setReceipt(null)}
        />
      )}
    </div>
  )
}

export default function DashboardPage() {
  return (
    <ProtectedRoute>
      <DashboardInner/>
    </ProtectedRoute>
  )
}
