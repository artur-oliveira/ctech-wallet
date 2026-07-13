'use client'

import {useQuery} from '@tanstack/react-query'
import {apiClient} from '@/lib/api/client'
import {entryLabel} from '@/lib/constants/ledger'
import {formatSigned} from '@/lib/utils/money'
import type {WalletType} from '@/lib/types/api'

const DATE = new Intl.DateTimeFormat('pt-BR', {day: '2-digit', month: 'short', hour: '2-digit', minute: '2-digit'})

export function LedgerList({type}: { type: WalletType }) {
  const real = type === 'real'
  const {data, isLoading, error} = useQuery({
    queryKey: ['ledger', type],
    queryFn: () => apiClient.getLedger(type),
  })
  
  if (isLoading) {
    return <p className="px-5 py-8 text-center text-sm text-gray-400">Carregando extrato...</p>
  }
  
  if (error) {
    return (
      <p className="px-5 py-8 text-center text-sm text-gray-500">
        Não foi possível carregar o extrato. Atualize a página para tentar de novo.
      </p>
    )
  }
  
  const items = data?.items ?? []
  
  if (items.length === 0) {
    return (
      <p className="px-5 py-10 text-center text-sm text-gray-500">
        {real
          ? 'Nenhuma movimentação ainda. Faça um depósito para começar.'
          : 'Nenhum crédito ainda. Compre créditos com seu saldo real para jogar.'}
      </p>
    )
  }
  
  return (
    <ul className="divide-y divide-gray-100">
      {items.map((entry) => (
        <li key={entry.entry_id} className="flex items-center justify-between px-5 py-3.5">
          <div>
            <p className="text-sm font-medium text-gray-900">{entryLabel(entry.type)}</p>
            <p className="mt-0.5 text-xs text-gray-500">{DATE.format(new Date(entry.created_at))}</p>
          </div>
          <p
            className={`font-mono text-sm tabular-nums ${
              entry.amount < 0 ? 'text-gray-600' : 'text-brand-700'
            }`}
          >
            {formatSigned(entry.amount, real)}
          </p>
        </li>
      ))}
    </ul>
  )
}
