'use client'

import {useCallback, useEffect, useRef, useState} from 'react'
import {useQueryClient} from '@tanstack/react-query'
import {toast} from 'sonner'
import {useTranslation} from 'react-i18next'
import {useWebSocket, type WSStatus} from '@aoctech/ws-client'
import {getAccessToken, refreshAccessToken, subscribeAccessToken} from '@/lib/api/client'
import type {RealtimeTransactionStatus} from '@/lib/utils/transaction-status'
import {realtimeAuthAction} from '@/lib/utils/realtime-auth'

// NEXT_PUBLIC_WS_URL is read, not derived, and that is the whole point: the
// generated CSP's connect-src is built from the `wss://` and `https://` literals
// present in the build environment, and connect-src is scheme-exact — allowing
// https://host does NOT allow wss://host. Deriving the socket origin from
// NEXT_PUBLIC_API_URL at runtime would leave no wss:// literal for the generator
// to find, and every socket would be blocked. It went unnoticed under CloudFront
// because the socket was same-origin and covered by 'self'.
//
// The API_URL fallback stays for local development, where nothing sets it.
const WS_BASE_URL = process.env.NEXT_PUBLIC_WS_URL || process.env.NEXT_PUBLIC_API_URL || ''

function buildWsUrl(): string {
  const origin = WS_BASE_URL || window.location.origin
  const base = origin.replace(/^http/, 'ws')
  return `${base}/v1.0/ws`
}

interface RealtimeMessage {
  type: string
  code?: string
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
  // Terminal means "do not reconnect with this credential", not "never again":
  // a genuinely new token clears it via the effect below.
  const [authTerminal, setAuthTerminal] = useState(false)
  const recoveriesRef = useRef(0)

  useEffect(() => subscribeAccessToken(() => {
    recoveriesRef.current = 0
    setAuthTerminal(false)
  }), [])

  const wsUrl = token && !authTerminal ? buildWsUrl() : null

  const handleMessage = useCallback((data: unknown) => {
    const msg = data as RealtimeMessage
    if (!msg?.type || msg.type === 'ping' || msg.type === 'connected') return

    const authAction = realtimeAuthAction(msg, {recoveries: recoveriesRef.current})
    if (authAction === 'stop') {
      setAuthTerminal(true)
      return
    }
    if (authAction === 'refresh') {
      recoveriesRef.current += 1
      // A successful refresh notifies the token subscribers above, which clears
      // the counter and reconnects with no backoff. A failed one takes the
      // socket down until the user's next authenticated request repairs the
      // session, which is where that repair belongs.
      void refreshAccessToken().then((renewed) => {
        if (!renewed) setAuthTerminal(true)
      })
      return
    }

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
