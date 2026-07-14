'use client'

import {useQuery} from '@tanstack/react-query'
import {useTranslation} from 'react-i18next'
import {apiClient} from '@/lib/api/client'
import {formatSigned} from '@/lib/utils/money'
import type {WalletType} from '@/lib/types/api'

export function LedgerList({type}: { type: WalletType }) {
  const {t, i18n} = useTranslation()
  const real = type === 'real'
  const {data, isLoading, error} = useQuery({
    queryKey: ['ledger', type],
    queryFn: () => apiClient.getLedger(type),
  })

  const dateFmt = new Intl.DateTimeFormat(i18n.language || 'pt-BR', {
    day: '2-digit',
    month: 'short',
    hour: '2-digit',
    minute: '2-digit',
  })

  if (isLoading) {
    return <p className="px-5 py-8 text-center text-sm text-gray-400">{t('dashboard.ledger.loading')}</p>
  }

  if (error) {
    return (
      <p className="px-5 py-8 text-center text-sm text-gray-500">
        {t('dashboard.ledger.error')}
      </p>
    )
  }

  const items = data?.items ?? []

  if (items.length === 0) {
    return (
      <p className="px-5 py-10 text-center text-sm text-gray-500">
        {real ? t('dashboard.ledger.emptyReal') : t('dashboard.ledger.emptyOther')}
      </p>
    )
  }

  return (
    <ul className="divide-y divide-gray-100">
      {items.map((entry) => (
        <li key={entry.entry_id} className="flex items-center justify-between px-5 py-3.5">
          <div>
            <p className="text-sm font-medium text-gray-900">{t(`ledger.type.${entry.type}`, entry.type)}</p>
            <p className="mt-0.5 text-xs text-gray-500">{dateFmt.format(new Date(entry.created_at))}</p>
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
