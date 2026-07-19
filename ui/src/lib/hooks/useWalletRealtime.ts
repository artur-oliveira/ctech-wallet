'use client'

import {useCallback} from 'react'
import {useQueryClient} from '@tanstack/react-query'
import {toast} from 'sonner'
import {useTranslation} from 'react-i18next'
import {useWebSocket, type WSStatus} from '@aoctech/ws-client'
import {getAccessToken, subscribeAccessToken} from '@/lib/api/client'
import type {RealtimeTransactionStatus} from '@/lib/utils/transaction-status'

// NEXT_PUBLIC_API_URL already carries the environment's API origin (set in
// frontend.yml) — converted http(s) → ws(s). Empty means same-origin, exactly
// like apiClient's own API_BASE_URL fallback.
const WS_BASE_URL = process.env.NEXT_PUBLIC_API_URL || ''

function buildWsUrl(): string {
    const origin = WS_BASE_URL || window.location.origin
    const base = origin.replace(/^http/, 'ws')
    return `${base}/v1.0/ws`
}

interface RealtimeMessage {
    type: string
    wallet_id?: string
    txid?: string
    withdrawal_id?: string
    amount?: number
}

/** Formats centavos as BRL without importing formatBRL, to keep this hook
 * dependency-free of the wallet component tree (avoids a circular import risk
 * between hooks/ and components/wallet/). */
function formatCentavos(amount: number, locale: string): string {
    return (amount / 100).toLocaleString(locale, {style: 'currency', currency: 'BRL'})
}

/** Withdrawal outcome events → toast key (mirrors api's broadcastWithdrawal). */
const WITHDRAW_TOAST_KEY: Record<string, string> = {
    withdraw_completed: 'toast.withdrawSent',
    withdraw_reversed: 'toast.withdrawReversed',
    withdraw_refund_failed: 'toast.withdrawRefundFailed',
}

const WITHDRAW_STATUS_EVENT: Record<string, RealtimeTransactionStatus['type']> = {
    withdraw_completed: 'withdraw_completed',
    withdraw_reversed: 'withdraw_reversed',
    withdraw_refund_failed: 'withdraw_refund_failed',
}

interface WalletRealtimeCallbacks {
    onDepositConfirmed?: (txid: string) => void
    onWithdrawalStatus?: (event: RealtimeTransactionStatus) => void
}

export function useWalletRealtime({
    onDepositConfirmed,
    onWithdrawalStatus,
}: WalletRealtimeCallbacks = {}): { wsStatus: WSStatus } {
    const {t, i18n} = useTranslation()
    const qc = useQueryClient()
    const token = getAccessToken()

    const wsUrl = token ? buildWsUrl() : null

    const handleMessage = useCallback((data: unknown) => {
        const msg = data as RealtimeMessage
        if (!msg?.type || msg.type === 'ping' || msg.type === 'connected') return

        if (msg.type === 'deposit_confirmed') {
            void qc.invalidateQueries({queryKey: ['balances']})
            void qc.invalidateQueries({queryKey: ['ledger']})
            toast.success(
                typeof msg.amount === 'number'
                    ? t('toast.realtimeDeposit', {amount: formatCentavos(msg.amount, i18n.language || 'pt-BR')})
                    : t('toast.depositConfirmed'),
            )
            if (msg.txid) onDepositConfirmed?.(msg.txid)
            return
        }

        const toastKey = WITHDRAW_TOAST_KEY[msg.type]
        if (toastKey) {
            void qc.invalidateQueries({queryKey: ['balances']})
            void qc.invalidateQueries({queryKey: ['ledger']})
            if (msg.type === 'withdraw_completed') {
                toast.success(t(toastKey))
            } else {
                toast.error(t(toastKey))
            }
            const statusType = WITHDRAW_STATUS_EVENT[msg.type]
            if (statusType && msg.withdrawal_id) {
                onWithdrawalStatus?.({type: statusType, transactionId: msg.withdrawal_id})
            }
        }
    }, [qc, t, i18n.language, onDepositConfirmed, onWithdrawalStatus])

    const {status: wsStatus} = useWebSocket({
        url: wsUrl,
        onMessage: handleMessage,
        enabled: !!wsUrl,
        authToken: token ?? undefined,
        subscribeToken: subscribeAccessToken,
    })

    return {wsStatus}
}
