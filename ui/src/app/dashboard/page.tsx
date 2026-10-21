'use client'

import {useState} from 'react'
import dynamic from 'next/dynamic'
import {useMutation, useQuery, useQueryClient} from '@tanstack/react-query'
import {toast} from 'sonner'
import {LogOut} from 'lucide-react'
import {useTranslation} from 'react-i18next'
import {apiClient, ApiError} from '@/lib/api/client'
import {formatBRL} from '@/lib/utils/money'
import {useAuth} from '@/lib/hooks/useAuth'
import {ProtectedRoute} from '@/components/protected-route'
import {BalanceCards} from '@/components/wallet/balance-cards'
import {LedgerTabs} from '@/components/wallet/ledger-tabs'
import {Button, Skeleton} from '@aoctech/ui'
import {QueryErrorState} from '@/components/query-error-state'
import {LanguageSwitcher} from '@/components/language-switcher'
import {useWalletRealtime} from '@/lib/hooks/useWalletRealtime'
import type {DepositResult} from '@/lib/types/api'
import Image from 'next/image'

type Flow = 'deposit' | 'withdraw' | null

const AmountDialog = dynamic(() => import('@/components/wallet/amount-dialog').then((module) => module.AmountDialog))
const ConfirmMoneyDialog = dynamic(() => import('@/components/wallet/confirm-money-dialog').then((module) => module.ConfirmMoneyDialog))
const PixChargeDialog = dynamic(() => import('@/components/wallet/pix-charge-dialog').then((module) => module.PixChargeDialog))
const MoneyReceiptDialog = dynamic(() => import('@/components/wallet/money-receipt-dialog').then((module) => module.MoneyReceiptDialog))

/** RFC 7807 problem type → i18n key. */
const PROBLEM_KEY: Record<string, string> = {
  '/problems/insufficient-balance': 'errors.insufficientBalance',
  '/problems/wallet-busy': 'errors.walletBusy',
  '/problems/withdraw-cpf-mismatch': 'errors.withdrawCpfMismatch',
  '/problems/pix-key-not-found': 'errors.pixKeyNotFound',
  '/problems/kyc-not-verified': 'errors.kycNotVerified',
  '/problems/step-up-required': 'errors.stepUpRequired',
  '/problems/daily-deposit-limit': 'errors.dailyDepositLimit',
  '/problems/daily-withdraw-limit': 'errors.dailyWithdrawLimit',
  '/problems/idempotency-conflict': 'errors.idempotencyConflict',
  '/problems/amount-above-limit': 'errors.amountAboveLimit',
  '/problems/account-blocked': 'errors.accountBlocked',
}

/** Turns an RFC 7807 problem from the API into copy the user can act on. */
function problemMessage(err: unknown, t: (k: string, o?: Record<string, unknown>) => string): string {
  if (!(err instanceof ApiError)) return t('common.genericError')
  if (err.type === '/problems/deposit-out-of-range') {
    const {min_amount: min, max_amount: max} = (err.raw ?? {}) as { min_amount?: number; max_amount?: number }
    if (min == null || max == null) return err.detail || t('errors.generic')
    return t('errors.depositOutOfRange', {min: formatBRL(min), max: formatBRL(max)})
  }
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
  const {profile, logout, reverify} = useAuth()
  const qc = useQueryClient()
  const [flow, setFlow] = useState<Flow>(null)
  const [confirm, setConfirm] = useState<{
    flow: 'withdraw';
    amount: number
  } | null>(null)
  const [charge, setCharge] = useState<DepositResult | null>(null)
  const [stepUp, setStepUp] = useState(false)
  const [receipt, setReceipt] = useState<{
    title: string
    amountLabel: string
    details?: Array<{ label: string; value: string }>
  } | null>(null)
  const balances = useQuery({queryKey: ['balances'], queryFn: () => apiClient.getBalances()})

  const {wsStatus} = useWalletRealtime({onDepositConfirmed: () => setCharge(null)})

  function refresh() {
    void qc.invalidateQueries({queryKey: ['balances']})
    void qc.invalidateQueries({queryKey: ['ledger']})
  }

  const deposit = useMutation({
    mutationFn: (amount: number) => apiClient.createDeposit(amount, newIdemKey()),
    onSuccess: (result) => {
      setFlow(null)
      setCharge(result)
    },
    onError: (err) => toast.error(problemMessage(err, t)),
  })

  const withdraw = useMutation({
    mutationFn: (amount: number) => apiClient.createWithdrawal(amount, newIdemKey()),
    onSuccess: (w) => {
      setConfirm(null)
      setFlow(null)
      refresh()
      if (w.status === 'processing') {
        toast.info(t('toast.withdrawProcessing'))
      } else {
        setReceipt({title: t('toast.withdrawSent'), amountLabel: formatBRL(w.amount)})
      }
    },
    onError: (err) => {
      if (err instanceof ApiError && err.type === '/problems/step-up-required') {
        setStepUp(true)
        return
      }
      toast.error(problemMessage(err, t))
    },
  })

  const name = profile?.first_name ?? profile?.username ?? ''

  return (
    <div className="min-h-screen bg-background">
      <header className="border-b border-border bg-card">
        <h1 className="sr-only">CTech Wallet</h1>
        <div className="mx-auto flex max-w-4xl items-center justify-between gap-3 px-4 py-4 sm:px-6">
          <div className="flex items-center gap-2.5">
            <div className="flex size-8 items-center justify-center rounded-lg bg-brand-600 text-white">
              <Image src="/app.svg"
                     alt=""
                     width={32}
                     height={32}/>
            </div>
            <span className="font-semibold text-foreground">CTech Wallet</span>
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
            <Button variant="ghost" size="icon" onClick={logout} aria-label={t('dashboard.logout')}>
              <LogOut size={16}/>
            </Button>
          </div>
        </div>
      </header>

      <main className="mx-auto max-w-4xl space-y-6 px-4 py-8 sm:px-6">
        {balances.isLoading && (
          <div role="status">
            <Skeleton className="h-44 rounded-2xl"/>
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
              onDeposit={() => setFlow('deposit')}
              onWithdraw={() => setFlow('withdraw')}
            />

            <LedgerTabs hasSandbox={!!balances.data.sandbox}/>
          </>
        )}
      </main>

      {flow === 'deposit' && (
        <AmountDialog
          flow="deposit"
          minCents={balances.data?.limits?.deposit.min}
          limitCents={balances.data?.limits?.deposit.max_now}
          pending={deposit.isPending}
          onSubmit={(amount) => deposit.mutate(amount)}
          onClose={() => setFlow(null)}
        />
      )}

      {flow === 'withdraw' && (
        <AmountDialog
          flow="withdraw"
          maxCents={balances.data?.real?.balance}
          minCents={balances.data?.limits?.withdraw.min}
          limitCents={balances.data?.limits?.withdraw.max_now}
          pending={withdraw.isPending || confirm?.flow === 'withdraw'}
          onProceed={(amount) => {
            setStepUp(false)
            setConfirm({flow: 'withdraw', amount})
          }}
          onClose={() => setFlow(null)}
        />
      )}

      {confirm && (
        <ConfirmMoneyDialog
          flow={confirm.flow}
          stepUp={stepUp}
          onReverify={reverify}
          amountCents={confirm.amount}
          availableCents={balances.data?.real?.balance ?? 0}
          pending={withdraw.isPending}
          onConfirm={() => withdraw.mutate(confirm.amount)}
          onClose={() => {
            setStepUp(false)
            setConfirm(null)
          }}
        />
      )}

      {charge && <PixChargeDialog deposit={charge} onClose={() => setCharge(null)}/>}

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
