'use client'

import {ArrowUpFromLine, Gamepad2, Plus} from 'lucide-react'
import {useTranslation} from 'react-i18next'
import {Button} from '@/components/ui/button'
import {formatBRL, formatCreditsAmount} from '@/lib/utils/money'
import type {Balances} from '@/lib/types/api'

interface BalanceCardsProps {
  balances: Balances
  onDeposit: () => void
  onWithdraw: () => void
}

/**
 * Two balances, and the hierarchy keeps them from being mistaken for one another:
 *
 * - Real money: solid filled violet card, R$ symbol, with deposit and withdraw.
 * - Sandbox credit: flat dashed card, whole-number balance with the virtual symbol ₡ (never R$),
 *   an explicit "não vira dinheiro" line, and no action: it has no monetary value, it can never be
 *   converted back, and it is bought and spent in Poker. The card shows only when a Sandbox wallet
 *   exists, as history.
 *
 * The game wallet is gone (it was merged into real) and so are consent, limits and self-exclusion
 * (Poker's), so there is no gambling surface here.
 */
export function BalanceCards({balances, onDeposit, onWithdraw}: BalanceCardsProps) {
  const {t} = useTranslation()
  const {sandbox} = balances

  return (
    <div className="space-y-4">
      {/* Real — money */}
      <section className="relative overflow-hidden rounded-2xl bg-brand-600 p-6 text-white">
        <p className="font-mono text-xs uppercase tracking-widest text-brand-50">{t('balance.real.label')}</p>
        <p className="mt-3 font-mono text-4xl font-bold tabular-nums tracking-tight">
          {formatBRL(balances.real.balance)}
        </p>
        <p className="mt-2 text-sm text-brand-50">{t('balance.real.subtitle')}</p>

        <div className="mt-6 flex flex-wrap gap-2">
          <Button variant="brand" onClick={onDeposit}>
            <Plus size={16}/>
            {t('balance.deposit')}
          </Button>
          <Button
            variant="outline"
            className="border-brand-400/60 bg-transparent text-white hover:bg-brand-700"
            onClick={onWithdraw}
          >
            <ArrowUpFromLine size={16}/>
            {t('balance.withdraw')}
          </Button>
        </div>
      </section>

      {/* Sandbox — not money */}
      {sandbox && (
        <section className="flex flex-col rounded-2xl border border-dashed border-border bg-card p-6">
          <div className="flex items-center gap-2">
            <Gamepad2 size={16} className="text-muted-foreground"/>
            <p
              className="font-mono text-xs uppercase tracking-widest text-muted-foreground">{t('balance.sandbox.label')}</p>
          </div>

          <p className="mt-3 font-mono text-3xl font-semibold tabular-nums tracking-tight text-foreground">
            {formatCreditsAmount(sandbox.balance)}
          </p>

          <p className="mt-2 text-sm leading-relaxed text-muted-foreground">
            {t('balance.sandbox.subtitle')}
          </p>
        </section>
      )}
    </div>
  )
}
