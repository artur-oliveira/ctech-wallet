'use client'

import Link from 'next/link'
import {ArrowDownToLine, ArrowUpFromLine, Dice5, Gamepad2, Plus, ShieldCheck} from 'lucide-react'
import {Button} from '@/components/ui/button'
import {formatBRL, formatCredits} from '@/lib/utils/money'
import type {Balances} from '@/lib/types/api'

interface BalanceCardsProps {
  balances: Balances
  onDeposit: () => void
  onWithdraw: () => void
  onBuyCredits: () => void
  onFundGame: () => void
  onReturnFromGame: () => void
}

/**
 * The balances are deliberately NOT rendered as a symmetric set — the visual
 * hierarchy encodes what each one actually is, so they cannot be mistaken for one
 * another at a glance:
 *
 * - Real money: solid filled violet card, R$ symbol.
 * - Game money is ALSO real money (withdrawable, via real), so it also carries R$ —
 *   but it is outlined rather than filled, marking it as ring-fenced: spendable
 *   only on games, and subject to the user's personal limits.
 * - Sandbox credit: flat dashed card, no currency symbol, explicit "não vira
 *   dinheiro" line — it has no monetary value and can never be converted back.
 *
 * A user who has not activated gambling sees ONLY the real card. Someone who came
 * here to pay a subscription is never shown a gambling surface — that is the point
 * of the opt-in, so the entry point is one quiet link, never a banner or an upsell.
 */
export function BalanceCards({
                               balances,
                               onDeposit,
                               onWithdraw,
                               onBuyCredits,
                               onFundGame,
                               onReturnFromGame,
                             }: BalanceCardsProps) {
  const {game, sandbox, activated} = balances
  
  return (
    <div className="space-y-4">
      <div className={activated ? 'grid gap-4 md:grid-cols-[1.4fr_1fr]' : 'grid gap-4'}>
        {/* Real — money */}
        <section className="relative overflow-hidden rounded-2xl bg-brand-600 p-6 text-white shadow-card">
          <div className="flex items-start justify-between">
            <div>
              <p className="font-mono text-xs uppercase tracking-widest text-brand-200">Saldo real</p>
              <p className="mt-3 text-4xl font-bold tabular-nums tracking-tight">
                {formatBRL(balances.real.balance)}
              </p>
              <p className="mt-2 text-sm text-brand-100">Depósito e saque via PIX</p>
            </div>
          </div>
          
          <div className="mt-6 flex flex-wrap gap-2">
            <Button
              variant="secondary"
              className="bg-white text-brand-700 hover:bg-brand-50"
              onClick={onDeposit}
            >
              <ArrowDownToLine size={16}/>
              Depositar
            </Button>
            <Button
              variant="outline"
              className="border-brand-400/60 bg-transparent text-white hover:bg-brand-500"
              onClick={onWithdraw}
            >
              <ArrowUpFromLine size={16}/>
              Sacar
            </Button>
            {activated && (
              <Button
                variant="outline"
                className="border-brand-400/60 bg-transparent text-white hover:bg-brand-500"
                onClick={onFundGame}
              >
                <Dice5 size={16}/>
                Enviar para jogos
              </Button>
            )}
          </div>
        </section>
        
        {/* Game — real money, ring-fenced */}
        {activated && game && (
          <section className="flex flex-col rounded-2xl border-2 border-brand-200 bg-white p-6">
            <div className="flex items-center gap-2">
              <Dice5 size={16} className="text-brand-500"/>
              <p className="font-mono text-xs uppercase tracking-widest text-brand-500">Saldo de jogo</p>
            </div>
            
            <p className="mt-3 text-3xl font-semibold tabular-nums tracking-tight text-gray-900">
              {formatBRL(game.balance)}
            </p>
            
            <p className="mt-2 text-sm leading-relaxed text-gray-500">
              Dinheiro real reservado para jogos. Pode voltar para o saldo real quando você quiser.
            </p>
            
            <div className="mt-auto flex flex-wrap gap-2 pt-6">
              <Button variant="outline" className="flex-1" onClick={onReturnFromGame}>
                <ArrowUpFromLine size={16}/>
                Devolver
              </Button>
              <Button variant="outline" className="flex-1" onClick={onBuyCredits}>
                <Plus size={16}/>
                Créditos
              </Button>
            </div>
          </section>
        )}
      </div>
      
      {/* Sandbox — not money */}
      {activated && sandbox && (
        <section className="flex flex-col rounded-2xl border border-dashed border-gray-300 bg-white p-6">
          <div className="flex items-center gap-2">
            <Gamepad2 size={16} className="text-gray-400"/>
            <p className="font-mono text-xs uppercase tracking-widest text-gray-400">Créditos sandbox</p>
          </div>
          
          <p className="mt-3 text-3xl font-semibold tabular-nums tracking-tight text-gray-700">
            {formatCredits(sandbox.balance)}
          </p>
          
          <p className="mt-2 text-sm leading-relaxed text-gray-500">
            Moeda virtual para partidas, comprada com o saldo de jogo. Não tem valor em dinheiro e não pode ser
            sacada nem convertida de volta.
          </p>
        </section>
      )}
      
      {/* Not activated — one quiet link, never an upsell. */}
      {!activated && (
        <Link
          href="/gambling/activate"
          className="flex items-center gap-2 rounded-xl border border-gray-200 bg-white px-4 py-3 text-sm text-gray-500 transition hover:border-gray-300 hover:text-gray-700"
        >
          <ShieldCheck size={16} className="text-gray-400"/>
          Vai usar a carteira para jogos? Ative a carteira de jogo.
        </Link>
      )}
    </div>
  )
}
