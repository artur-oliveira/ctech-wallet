'use client'

import {useEffect, useState} from 'react'
import {useQuery} from '@tanstack/react-query'
import {Check, Copy} from 'lucide-react'
import {toast} from 'sonner'
import {Button} from '@/components/ui/button'
import {formatBRL} from '@/lib/utils/money'
import {apiClient} from '@/lib/api/client'
import type {DepositResult} from '@/lib/types/api'

const POLL_DELAY_MS = 30_000 // start polling only after this — the WS path is primary
const POLL_INTERVAL_MS = 5_000

/**
 * Shown after a deposit is opened. The charge expires in 15 minutes and the
 * balance only updates after the bank confirms the payment — so the copy is
 * explicit that closing this window is safe.
 */
export function PixChargeDialog(
  {deposit, initialRealBalance, onClose, onConfirmed}: {
    deposit: DepositResult
    initialRealBalance: number
    onClose: () => void
    onConfirmed: () => void
  },
) {
  const [copied, setCopied] = useState(false)
  const [polling, setPolling] = useState(false)

  useEffect(() => {
    const t = setTimeout(() => setPolling(true), POLL_DELAY_MS)
    return () => clearTimeout(t)
  }, [])

  const balances = useQuery({
    queryKey: ['balances'],
    queryFn: () => apiClient.getBalances(),
    enabled: polling,
    refetchInterval: polling ? POLL_INTERVAL_MS : false,
  })

  useEffect(() => {
    const realBalance = balances.data?.real?.balance
    if (realBalance != null && realBalance >= initialRealBalance + deposit.amount) {
      toast.success('Depósito confirmado')
      onConfirmed()
    }
  }, [balances.data, initialRealBalance, deposit.amount, onConfirmed])

  async function copy() {
    await navigator.clipboard.writeText(deposit.pix_copia_e_cola)
    setCopied(true)
    toast.success('Código PIX copiado')
    setTimeout(() => setCopied(false), 2000)
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-gray-900/40 p-4">
      <div className="w-full max-w-sm rounded-2xl bg-white p-6 shadow-modal">
        <h2 className="text-lg font-semibold text-gray-900">Pague {formatBRL(deposit.amount)} no PIX</h2>
        <p className="mt-1 text-sm leading-relaxed text-gray-500">
          Copie o código e pague no app do seu banco. O saldo entra assim que o banco confirmar o pagamento.
        </p>
        
        {deposit.qr_code_base64 && (
          // eslint-disable-next-line @next/next/no-img-element
          <img
            src={`data:image/png;base64,${deposit.qr_code_base64}`}
            alt="QR Code do pagamento PIX"
            className="mx-auto mt-5 size-44 rounded-lg border border-gray-200"
          />
        )}
        
        <p className="mt-5 break-all rounded-lg bg-gray-50 p-3 font-mono text-xs leading-relaxed text-gray-600">
          {deposit.pix_copia_e_cola}
        </p>
        
        <Button variant="brand" className="mt-3 w-full" onClick={copy}>
          {copied ? <Check size={16}/> : <Copy size={16}/>}
          {copied ? 'Copiado' : 'Copiar código PIX'}
        </Button>
        
        <p className="mt-3 text-center text-xs text-gray-500">
          O código vale por 15 minutos. Pode fechar esta janela — o pagamento continua valendo.
        </p>
        
        <Button variant="ghost" className="mt-2 w-full" onClick={onClose}>
          Fechar
        </Button>
      </div>
    </div>
  )
}
