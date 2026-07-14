'use client'

import {useState} from 'react'
import {useMutation, useQuery, useQueryClient} from '@tanstack/react-query'
import {toast} from 'sonner'
import {LogOut} from 'lucide-react'
import {useTranslation} from 'react-i18next'
import {apiClient, ApiError} from '@/lib/api/client'
import {formatBRL} from '@/lib/utils/money'
import {useAuth} from '@/lib/hooks/useAuth'
import {ProtectedRoute} from '@/components/protected-route'
import {BalanceCards} from '@/components/wallet/balance-cards'
import {LedgerList} from '@/components/wallet/ledger-list'
import {AmountDialog} from '@/components/wallet/amount-dialog'
import {ConfirmMoneyDialog} from '@/components/wallet/confirm-money-dialog'
import {PixChargeDialog} from '@/components/wallet/pix-charge-dialog'
import {Button} from '@/components/ui/button'
import {useWalletRealtime} from '@/lib/hooks/useWalletRealtime'
import type {DepositResult, WalletType} from '@/lib/types/api'
import Image from 'next/image'

type Flow = 'deposit' | 'withdraw' | 'credits' | 'fund-game' | 'return-game' | null

/** Game and sandbox statements exist only once the user has activated gambling. */
const LEDGER_TABS = (activated: boolean): WalletType[] =>
  activated ? ['real', 'game', 'sandbox'] : ['real']

/** RFC 7807 problem type → i18n key. */
const PROBLEM_KEY: Record<string, string> = {
  '/problems/insufficient-balance': 'errors.insufficientBalance',
  '/problems/wallet-busy': 'errors.walletBusy',
  '/problems/withdraw-cpf-mismatch': 'errors.withdrawCpfMismatch',
  '/problems/pix-key-not-found': 'errors.pixKeyNotFound',
  '/problems/kyc-not-verified': 'errors.kycNotVerified',
  '/problems/step-up-required': 'errors.stepUpRequired',
  '/problems/idempotency-conflict': 'errors.idempotencyConflict',
  '/problems/gambling-not-activated': 'errors.gamblingNotActivated',
  '/problems/gambling-terms-required': 'errors.gamblingTermsRequired',
  '/problems/amount-above-limit': 'errors.amountAboveLimit',
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
  const {profile, logout} = useAuth()
  const qc = useQueryClient()
  useWalletRealtime()
  const [flow, setFlow] = useState<Flow>(null)
  const [confirm, setConfirm] = useState<{ flow: 'withdraw' | 'fund-game' | 'return-game'; amount: number } | null>(null)
  const [pendingPixKey, setPendingPixKey] = useState<string>('')
  const [charge, setCharge] = useState<DepositResult | null>(null)
  const [tab, setTab] = useState<WalletType>('real')

  const balances = useQuery({queryKey: ['balances'], queryFn: () => apiClient.getBalances()})

  function refresh() {
    void qc.invalidateQueries({queryKey: ['balances']})
    void qc.invalidateQueries({queryKey: ['ledger']})
  }

  const deposit = useMutation({
    mutationFn: (amount: number) => apiClient.createDeposit(amount),
    onSuccess: (result) => {
      setFlow(null)
      setCharge(result)
    },
    onError: (err) => toast.error(problemMessage(err, t)),
  })

  const withdraw = useMutation({
    mutationFn: ({amount, pixKey}: { amount: number; pixKey: string }) =>
      apiClient.createWithdrawal(amount, pixKey, newIdemKey()),
    onSuccess: (w) => {
      setConfirm(null)
      refresh()
      toast.success(
        w.status === 'processing'
          ? t('toast.withdrawProcessing')
          : t('toast.withdrawSent'),
      )
    },
    onError: (err) => toast.error(problemMessage(err, t)),
  })

  const buyCredits = useMutation({
    mutationFn: (amount: number) => apiClient.purchaseSandbox(amount, newIdemKey()),
    onSuccess: () => {
      setFlow(null)
      refresh()
      toast.success(t('toast.creditsAdded'))
    },
    onError: (err) => toast.error(problemMessage(err, t)),
  })

  const fundGame = useMutation({
    mutationFn: (amount: number) => apiClient.fundGame(amount, newIdemKey()),
    onSuccess: () => {
      setConfirm(null)
      refresh()
      toast.success(t('toast.fundGameSent'))
    },
    onError: (err) => toast.error(problemMessage(err, t)),
  })

  const returnFromGame = useMutation({
    mutationFn: (amount: number) => apiClient.returnFromGame(amount, newIdemKey()),
    onSuccess: () => {
      setConfirm(null)
      refresh()
      toast.success(t('toast.returned'))
    },
    onError: (err) => toast.error(problemMessage(err, t)),
  })

  const name = profile?.first_name ?? profile?.username ?? ''

  return (
    <div className="min-h-screen bg-background">
      <header className="border-b border-border bg-card">
        <div className="mx-auto flex max-w-4xl items-center justify-between px-6 py-4">
          <div className="flex items-center gap-2.5">
            <div className="flex size-8 items-center justify-center rounded-lg bg-brand-600 text-white">
              <Image src="/app.svg"
                     alt="Wallet"
                     width={32}
                     height={32}/>
            </div>
            <span className="font-semibold text-foreground">CTech Wallet</span>
          </div>
          <div className="flex items-center gap-3">
            {name && <span className="hidden text-sm text-muted-foreground sm:inline">{name}</span>}
            <Button variant="ghost" size="icon-sm" onClick={logout} aria-label={t('dashboard.logout')}>
              <LogOut size={16}/>
            </Button>
          </div>
        </div>
      </header>

      <main className="mx-auto max-w-4xl space-y-6 px-6 py-8">
        {balances.isLoading && (
          <div className="h-44 animate-pulse rounded-2xl bg-muted" aria-label={t('dashboard.loadingBalances')}/>
        )}

        {balances.error && (
          <p className="rounded-xl border border-border bg-card p-5 text-sm text-muted-foreground">
            {t('dashboard.loadError')}
          </p>
        )}

        {balances.data && (
          <>
            <BalanceCards
              balances={balances.data}
              onDeposit={() => setFlow('deposit')}
              onWithdraw={() => setFlow('withdraw')}
              onBuyCredits={() => setFlow('credits')}
              onFundGame={() => setFlow('fund-game')}
              onReturnFromGame={() => setFlow('return-game')}
            />

            <section className="overflow-hidden rounded-xl border border-border bg-card">
              <div className="flex overflow-x-auto border-b border-border">
                {LEDGER_TABS(balances.data.activated).map((tk) => (
                  <button
                    key={tk}
                    onClick={() => setTab(tk)}
                    className={`px-5 py-3.5 text-xs font-semibold uppercase tracking-wider transition-colors whitespace-nowrap ${
                      tab === tk
                        ? 'border-b-2 border-brand-600 text-brand-700'
                        : 'text-muted-foreground hover:text-foreground'
                    }`}
                  >
                    {t(`dashboard.ledger.tab.${tk}`)}
                  </button>
                ))}
              </div>
              <LedgerList type={tab}/>
            </section>
          </>
        )}
      </main>

      {flow === 'deposit' && (
        <AmountDialog
          flow="deposit"
          pending={deposit.isPending}
          onSubmit={(amount) => deposit.mutate(amount)}
          onClose={() => setFlow(null)}
        />
      )}

      {flow === 'withdraw' && (
        <AmountDialog
          flow="withdraw"
          maxCents={balances.data?.real?.balance}
          pending={withdraw.isPending || confirm?.flow === 'withdraw'}
          onProceed={(amount, pixKey) => {
            setPendingPixKey(pixKey ?? '')
            setConfirm({flow: 'withdraw', amount})
          }}
          onClose={() => setFlow(null)}
        />
      )}

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
          pending={
            confirm.flow === 'withdraw'
              ? withdraw.isPending
              : confirm.flow === 'fund-game'
                ? fundGame.isPending
                : returnFromGame.isPending
          }
          onConfirm={() => {
            if (confirm.flow === 'withdraw') {
              withdraw.mutate({amount: confirm.amount, pixKey: pendingPixKey})
            } else if (confirm.flow === 'fund-game') {
              fundGame.mutate(confirm.amount)
            } else {
              returnFromGame.mutate(confirm.amount)
            }
          }}
          onClose={() => setConfirm(null)}
        />
      )}

      {charge && (
        <PixChargeDialog
          deposit={charge}
          initialRealBalance={balances.data?.real?.balance ?? 0}
          onClose={() => setCharge(null)}
          onConfirmed={() => setCharge(null)}
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
