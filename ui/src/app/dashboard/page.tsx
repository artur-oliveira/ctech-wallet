'use client'

import {useCallback, useEffect, useMemo, useState} from 'react'
import {useMutation, useQuery, useQueryClient} from '@tanstack/react-query'
import {toast} from 'sonner'
import {LogOut} from 'lucide-react'
import {useTranslation} from 'react-i18next'
import {apiClient, ApiError} from '@/lib/api/client'
import {formatBRL, formatCredits} from '@/lib/utils/money'
import {useAuth} from '@/lib/hooks/useAuth'
import {ProtectedRoute} from '@/components/protected-route'
import {BalanceCards} from '@/components/wallet/balance-cards'
import {LedgerList} from '@/components/wallet/ledger-list'
import {AmountDialog} from '@/components/wallet/amount-dialog'
import {ConfirmMoneyDialog} from '@/components/wallet/confirm-money-dialog'
import {MoneyReceiptDialog} from '@/components/wallet/money-receipt-dialog'
import {PixChargeDialog} from '@/components/wallet/pix-charge-dialog'
import {TransactionStatusList} from '@/components/wallet/transaction-status-list'
import {Button} from '@/components/ui/button'
import {useWalletRealtime} from '@/lib/hooks/useWalletRealtime'
import type {DepositResult, WalletType} from '@/lib/types/api'
import {
    applyRealtimeStatus,
    parseStoredDeposit,
    parseTransactionHistory,
    reconcileTransactionHistory,
    upsertTransaction,
    type TrackedTransaction,
} from '@/lib/utils/transaction-status'
import {SESSION_KEY_PIX_CHARGE_PREFIX, SESSION_KEY_TRANSACTION_PREFIX} from '@/lib/constants/storage'
import Image from 'next/image'

type Flow = 'deposit' | 'withdraw' | 'credits' | 'fund-game' | 'return-game' | null

/** Game and sandbox statements exist only once the user has activated gambling. */
const LEDGER_TABS = (activated: boolean): WalletType[] =>
    activated ? ['real', 'game', 'sandbox'] : ['real']

const TRANSACTION_POLL_INTERVAL_MS = 10_000

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
    '/problems/self-excluded': 'errors.selfExcluded',
    '/problems/limits-not-configured': 'errors.limitsNotConfigured',
    '/problems/deposit-limit-exceeded': 'errors.depositLimitExceeded',
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
        flow: 'withdraw' | 'fund-game' | 'return-game';
        amount: number
    } | null>(null)
    const [charge, setCharge] = useState<DepositResult | null>(null)
    const [chargeOpen, setChargeOpen] = useState(false)
    const [transactions, setTransactions] = useState<TrackedTransaction[]>([])
    const [hydratedStorageKey, setHydratedStorageKey] = useState<string | null>(null)
    const [receipt, setReceipt] = useState<{
        title: string
        amountLabel: string
        details?: Array<{label: string; value: string}>
    } | null>(null)
    const [stepUp, setStepUp] = useState(false)
    const [tab, setTab] = useState<WalletType>('real')

    const balances = useQuery({queryKey: ['balances'], queryFn: () => apiClient.getBalances()})
    const responsible = useQuery({queryKey: ['gambling-limits'], queryFn: () => apiClient.getGameLimits()})
    const walletID = balances.data?.real.wallet_id
    const transactionStorageKey = walletID ? `${SESSION_KEY_TRANSACTION_PREFIX}${walletID}` : null
    const chargeStorageKey = walletID ? `${SESSION_KEY_PIX_CHARGE_PREFIX}${walletID}` : null

    const handleDepositConfirmed = useCallback((txid: string) => {
        setTransactions((current) => applyRealtimeStatus(current, {
            type: 'deposit_confirmed',
            transactionId: txid,
        }))
        setCharge((current) => current?.txid === txid ? null : current)
        setChargeOpen(false)
        void qc.invalidateQueries({queryKey: ['balances']})
        void qc.invalidateQueries({queryKey: ['ledger']})
    }, [qc])

    const handleWithdrawalStatus = useCallback((event: Parameters<typeof applyRealtimeStatus>[1]) => {
        setTransactions((current) => applyRealtimeStatus(current, event))
    }, [])

    const {wsStatus} = useWalletRealtime({
        onDepositConfirmed: handleDepositConfirmed,
        onWithdrawalStatus: handleWithdrawalStatus,
    })

    useEffect(() => {
        if (!transactionStorageKey || !chargeStorageKey) return
        const hydrate = window.setTimeout(() => {
            const storedTransactions = parseTransactionHistory(sessionStorage.getItem(transactionStorageKey))
            setTransactions(storedTransactions)

            const rawCharge = sessionStorage.getItem(chargeStorageKey)
            const storedCharge = parseStoredDeposit(rawCharge)
            if (rawCharge && !storedCharge) sessionStorage.removeItem(chargeStorageKey)
            setCharge(storedCharge)
            setChargeOpen(false)
            setHydratedStorageKey(transactionStorageKey)
        }, 0)
        return () => window.clearTimeout(hydrate)
    }, [chargeStorageKey, transactionStorageKey])

    const hasUnresolvedTransaction = transactions.some(
        (item) => item.status === 'pending' || item.status === 'processing',
    )
    const transactionLedger = useQuery({
        queryKey: ['transaction-status-ledger', walletID],
        queryFn: () => apiClient.getLedger('real', undefined, 200),
        enabled: hydratedStorageKey === transactionStorageKey && !!walletID && hasUnresolvedTransaction,
        refetchInterval: hasUnresolvedTransaction ? TRANSACTION_POLL_INTERVAL_MS : false,
    })

    const visibleTransactions = useMemo(
        () => transactionLedger.data
            ? reconcileTransactionHistory(
                transactions,
                transactionLedger.data.items,
                transactionLedger.dataUpdatedAt,
            )
            : transactions,
        [transactionLedger.data, transactionLedger.dataUpdatedAt, transactions],
    )
    const activeCharge = charge && visibleTransactions.some(
        (item) => item.id === charge.txid && item.status === 'pending',
    ) ? charge : null

    useEffect(() => {
        if (hydratedStorageKey !== transactionStorageKey || !transactionStorageKey || !chargeStorageKey) return
        sessionStorage.setItem(transactionStorageKey, JSON.stringify(visibleTransactions))
        if (activeCharge) sessionStorage.setItem(chargeStorageKey, JSON.stringify(activeCharge))
        else sessionStorage.removeItem(chargeStorageKey)
    }, [activeCharge, chargeStorageKey, hydratedStorageKey, transactionStorageKey, visibleTransactions])

    function refresh() {
        void qc.invalidateQueries({queryKey: ['balances']})
        void qc.invalidateQueries({queryKey: ['ledger']})
    }

    const deposit = useMutation({
        mutationFn: (amount: number) => apiClient.createDeposit(amount),
        onSuccess: (result) => {
            setFlow(null)
            setCharge(result)
            setChargeOpen(true)
            setTransactions((current) => upsertTransaction(current, {
                id: result.txid,
                kind: 'deposit',
                amount: result.amount,
                status: 'pending',
                created_at: new Date().toISOString(),
                expires_at: result.expires_at,
            }))
        },
        onError: (err) => toast.error(problemMessage(err, t)),
    })

    const withdraw = useMutation({
        mutationFn: (amount: number) => apiClient.createWithdrawal(amount, newIdemKey()),
        onSuccess: (w) => {
            setConfirm(null)
            setFlow(null)
            refresh()
            setTransactions((current) => upsertTransaction(current, {
                id: w.withdrawal_id,
                kind: 'withdrawal',
                amount: w.amount,
                fee: w.fee,
                status: w.status,
                created_at: w.created_at,
                updated_at: w.updated_at,
            }))
            if (w.status === 'processing') {
                toast.info(t('toast.withdrawProcessing'))
            } else {
                setReceipt({
                    title: t('toast.withdrawSent'),
                    amountLabel: formatBRL(w.amount),
                    details: [
                        {label: t('confirm.fee'), value: formatBRL(w.fee)},
                        {label: t('confirm.total'), value: formatBRL(w.amount + w.fee)},
                    ],
                })
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

    const buyCredits = useMutation({
        mutationFn: (amount: number) => apiClient.purchaseSandbox(amount, newIdemKey()),
        onSuccess: (transfer) => {
            setFlow(null)
            refresh()
            setReceipt({title: t('toast.creditsAdded'), amountLabel: formatCredits(transfer.credit.amount)})
        },
        onError: (err) => toast.error(problemMessage(err, t)),
    })

    const fundGame = useMutation({
        mutationFn: (amount: number) => apiClient.fundGame(amount, newIdemKey()),
        onSuccess: (transfer) => {
            setConfirm(null)
            setFlow(null)
            refresh()
            setReceipt({title: t('toast.fundGameSent'), amountLabel: formatBRL(transfer.credit.amount)})
        },
        onError: (err) => toast.error(problemMessage(err, t)),
    })

    const returnFromGame = useMutation({
        mutationFn: (amount: number) => apiClient.returnFromGame(amount, newIdemKey()),
        onSuccess: (transfer) => {
            setConfirm(null)
            setFlow(null)
            refresh()
            setReceipt({title: t('toast.returned'), amountLabel: formatBRL(transfer.credit.amount)})
        },
        onError: (err) => toast.error(problemMessage(err, t)),
    })

    const name = profile?.first_name ?? profile?.username ?? ''

    return (
        <div className="min-h-screen bg-background">
            <header className="border-b border-border bg-card">
                <h1 className="sr-only">CTech Wallet</h1>
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
                            <span className="text-xs text-muted-foreground">
                                {wsStatus === 'connected'
                                    ? t('dashboard.live')
                                    : wsStatus === 'connecting'
                                        ? t('dashboard.connecting')
                                        : t('dashboard.offline')}
                            </span>
                        </span>
                        {name && <span className="hidden max-w-[10rem] truncate text-sm text-muted-foreground sm:inline">{name}</span>}
                        <Button variant="ghost" size="icon-sm" onClick={logout} aria-label={t('dashboard.logout')}>
                            <LogOut size={16}/>
                        </Button>
                    </div>
                </div>
            </header>

            <main className="mx-auto max-w-4xl space-y-6 px-6 py-8">
                {balances.isLoading && (
                    <div className="h-44 animate-pulse rounded-2xl bg-muted"
                         aria-label={t('dashboard.loadingBalances')}/>
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

                        <TransactionStatusList
                            transactions={visibleTransactions}
                            activeDepositId={activeCharge?.txid}
                            onResumeDeposit={(txid) => {
                                if (activeCharge?.txid === txid) setChargeOpen(true)
                            }}
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
                    feeConfig={balances.data?.real}
                    pending={withdraw.isPending || confirm?.flow === 'withdraw'}
                    onProceed={(amount) => {
                        setStepUp(false)
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
                    stepUp={stepUp}
                    onReverify={reverify}
                    amountCents={confirm.amount}
                    availableCents={
                        confirm.flow === 'return-game'
                            ? balances.data?.game?.balance ?? 0
                            : balances.data?.real?.balance ?? 0
                    }
                    feeConfig={balances.data?.real}
                    pending={
                        confirm.flow === 'withdraw'
                            ? withdraw.isPending
                            : confirm.flow === 'fund-game'
                                ? fundGame.isPending
                                : returnFromGame.isPending
                    }
                    onConfirm={() => {
                        if (confirm.flow === 'withdraw') {
                            withdraw.mutate(confirm.amount)
                        } else if (confirm.flow === 'fund-game') {
                            fundGame.mutate(confirm.amount)
                        } else {
                            returnFromGame.mutate(confirm.amount)
                        }
                    }}
                    onClose={() => { setStepUp(false); setConfirm(null) }}
                />
            )}

            {activeCharge && chargeOpen && (
                <PixChargeDialog
                    deposit={activeCharge}
                    onClose={() => setChargeOpen(false)}
                    onConfirmed={() => handleDepositConfirmed(activeCharge.txid)}
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
