'use client'

import {useCallback} from 'react'
import {useQueryClient} from '@tanstack/react-query'
import {toast} from 'sonner'
import {useWebSocket, type WSStatus} from './useWebSocket'
import {getAccessToken} from '@/lib/api/client'

// NEXT_PUBLIC_API_URL already carries the environment's API origin (set in
// frontend.yml) — converted http(s) → ws(s). Empty means same-origin, exactly
// like apiClient's own API_BASE_URL fallback.
const WS_BASE_URL = process.env.NEXT_PUBLIC_API_URL || ''

function buildWsUrl(token: string): string {
  const origin = WS_BASE_URL || window.location.origin
  const base = origin.replace(/^http/, 'ws')
  return `${base}/v1.0/ws?token=${encodeURIComponent(token)}`
}

interface RealtimeMessage {
  type: string
  wallet_id?: string
  txid?: string
  amount?: number
}

/** Formats centavos as BRL without importing formatBRL, to keep this hook
 * dependency-free of the wallet component tree (avoids a circular import risk
 * between hooks/ and components/wallet/). */
function formatCentavos(amount: number): string {
  return (amount / 100).toLocaleString('pt-BR', {style: 'currency', currency: 'BRL'})
}

export function useWalletRealtime(): { wsStatus: WSStatus } {
  const qc = useQueryClient()
  const token = getAccessToken()

  const wsUrl = token ? buildWsUrl(token) : null

  const handleMessage = useCallback((data: unknown) => {
    const msg = data as RealtimeMessage
    if (!msg?.type || msg.type === 'ping' || msg.type === 'connected') return

    if (msg.type === 'deposit_confirmed') {
      void qc.invalidateQueries({queryKey: ['balances']})
      void qc.invalidateQueries({queryKey: ['ledger']})
      const amount = typeof msg.amount === 'number' ? ` de ${formatCentavos(msg.amount)}` : ''
      toast.success(`Depósito${amount} confirmado`)
    }
  }, [qc])

  const {status: wsStatus} = useWebSocket({
    url: wsUrl,
    onMessage: handleMessage,
    enabled: !!wsUrl,
  })

  return {wsStatus}
}
