'use client'

import Link from 'next/link'
import {ArrowDownToLine, ArrowUpFromLine, Dice5, Gamepad2, Plus, ShieldCheck} from 'lucide-react'
import {useTranslation} from 'react-i18next'
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
    selfExcluded?: boolean
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
                                 selfExcluded,
                             }: BalanceCardsProps) {
    const {t} = useTranslation()
    const {game, sandbox, activated} = balances

    return (
        <div className="space-y-4">
            <div className={activated ? 'grid gap-4 md:grid-cols-[1.4fr_1fr]' : 'grid gap-4'}>
                {/* Real — money */}
                <section className="relative overflow-hidden rounded-2xl bg-brand-600 p-6 text-white">
                    <div className="flex items-start justify-between">
                        <div>
                            <p className="font-mono text-xs uppercase tracking-widest text-brand-50">{t('balance.real.label')}</p>
                            <p className="mt-3 font-mono text-4xl font-bold tabular-nums tracking-tight">
                                {formatBRL(balances.real.balance)}
                            </p>
                            <p className="mt-2 text-sm text-brand-50">{t('balance.real.subtitle')}</p>
                        </div>
                    </div>

                    <div className="mt-6 flex flex-wrap gap-2">
                        <Button
                            variant="secondary"
                            className="bg-white text-brand-700 hover:bg-brand-50"
                            onClick={onDeposit}
                        >
                            <ArrowDownToLine size={16}/>
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
                        {activated && !selfExcluded && (
                            <Button
                                variant="outline"
                                className="border-brand-400/60 bg-transparent text-white hover:bg-brand-700"
                                onClick={onFundGame}
                            >
                                <Dice5 size={16}/>
                                {t('balance.fundGame')}
                            </Button>
                        )}
                    </div>
                </section>

                {/* Game — real money, ring-fenced */}
                {activated && game && (
                    <section className="flex flex-col rounded-2xl border-2 border-brand-200 bg-card p-6">
                        <div className="flex flex-wrap items-center gap-2">
                            <Dice5 size={16} className="text-brand-600"/>
                            <p className="font-mono text-xs uppercase tracking-widest text-brand-600">{t('balance.game.label')}</p>
                            <span className="rounded-full bg-brand-50 px-2 py-0.5 text-xs font-medium text-brand-700">
                {t('balance.game.badge')}
              </span>
                        </div>

                        <p className="mt-3 font-mono text-3xl font-semibold tabular-nums tracking-tight text-foreground">
                            {formatBRL(game.balance)}
                        </p>

                        <p className="mt-2 text-sm leading-relaxed text-muted-foreground">
                            {t('balance.game.subtitle')}
                        </p>

                        <div className="mt-auto flex flex-wrap gap-2 pt-6">
                            <Button variant="outline" className="flex-1" onClick={onReturnFromGame}>
                                <ArrowUpFromLine size={16}/>
                                {t('balance.return')}
                            </Button>
                            <Button variant="outline" className="flex-1" onClick={onBuyCredits}>
                                <Plus size={16}/>
                                {t('balance.credits')}
                            </Button>
                        </div>
                    </section>
                )}
            </div>

            {/* Sandbox — not money */}
            {activated && sandbox && (
                <section className="flex flex-col rounded-2xl border border-dashed border-border bg-card p-6">
                    <div className="flex items-center gap-2">
                        <Gamepad2 size={16} className="text-muted-foreground"/>
                        <p className="font-mono text-xs uppercase tracking-widest text-muted-foreground">{t('balance.sandbox.label')}</p>
                    </div>

                    <p className="mt-3 font-mono text-3xl font-semibold tabular-nums tracking-tight text-foreground">
                        {formatCredits(sandbox.balance)}
                    </p>

                    <p className="mt-2 text-sm leading-relaxed text-muted-foreground">
                        {t('balance.sandbox.subtitle')}
                    </p>
                </section>
            )}

            {activated && (
                <Link href="/gambling/responsible" className="flex items-center gap-2 text-sm font-medium text-brand-700 hover:underline">
                    <ShieldCheck size={16}/>{t('balance.responsibleLink')}
                </Link>
            )}

            {/* Not activated — one quiet link, never an upsell. */}
            {!activated && (
                <Link
                    href="/gambling/activate"
                    className="flex items-center gap-2 rounded-xl border border-border bg-card px-4 py-3 text-sm text-muted-foreground transition-colors hover:border-brand-200 hover:text-foreground"
                >
                    <ShieldCheck size={16} className="text-muted-foreground"/>
                    {t('balance.activateLink')}
                </Link>
            )}
        </div>
    )
}
