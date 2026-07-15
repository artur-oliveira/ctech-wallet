'use client'

import {useCallback, useEffect, useRef} from 'react'
import {useTranslation} from 'react-i18next'
import {Button} from '@/components/ui/button'
import {formatBRL} from '@/lib/utils/money'
import {withdrawalFee} from '@/lib/utils/fee'

type Flow = 'withdraw' | 'fund-game' | 'return-game'

const FLOW_KEY: Record<Flow, 'withdraw' | 'fundGame' | 'returnGame'> = {
    withdraw: 'withdraw',
    'fund-game': 'fundGame',
    'return-game': 'returnGame',
}

interface ConfirmMoneyDialogProps {
    flow: Flow
    amountCents: number
    /** Available balance of the wallet being debited (real for withdraw/fund-game, game for return-game). */
    availableCents: number
    pending?: boolean
    /** When true, the API rejected the commit with step-up-required: show an in-flow re-verify step. */
    stepUp?: boolean
    /** Re-verifies identity (MFA) via the OAuth re-auth flow, then the user retries. */
    onReverify?: () => void
    onConfirm: () => void
    onClose: () => void
}

/**
 * Two-step commit for real-money moves. The amount is entered in AmountDialog;
 * this review step makes the cost explicit before anything leaves the wallet —
 * the withdrawal fee (2%, floored at R$ 1) is shown here, never hidden.
 */
export function ConfirmMoneyDialog({
                                       flow,
                                       amountCents,
                                       availableCents,
                                       pending,
                                       stepUp,
                                       onReverify,
                                       onConfirm,
                                       onClose,
                                   }: ConfirmMoneyDialogProps) {
    const {t} = useTranslation()
    const confirmRef = useRef<HTMLButtonElement>(null)
    const reverifyRef = useRef<HTMLButtonElement>(null)
    const panelRef = useRef<HTMLDivElement>(null)

    const isWithdraw = flow === 'withdraw'
    const fee = isWithdraw ? withdrawalFee(amountCents) : 0
    const totalDebit = amountCents + fee
    const resultingBalance = Math.max(0, availableCents - totalDebit)

    const flowKey = FLOW_KEY[flow]
    const titleKey = `confirm.${flowKey}.title`
    const descKey = `confirm.${flowKey}.description`

    const onKeyDown = useCallback(
        (e: React.KeyboardEvent) => {
            if (e.key === 'Escape' && !pending) {
                e.preventDefault()
                onClose()
                return
            }
            if (e.key === 'Tab') {
                const focusables = panelRef.current?.querySelectorAll<HTMLElement>(
                    'button:not([disabled]), [href], input, select, textarea, [tabindex]:not([tabindex="-1"])',
                )
                if (!focusables || focusables.length === 0) return
                const first = focusables[0]
                const last = focusables[focusables.length - 1]
                if (e.shiftKey && document.activeElement === first) {
                    e.preventDefault()
                    last.focus()
                } else if (!e.shiftKey && document.activeElement === last) {
                    e.preventDefault()
                    first.focus()
                }
            }
        },
        [onClose, pending],
    )

    useEffect(() => {
        const previouslyFocused = document.activeElement as HTMLElement | null
        if (stepUp) reverifyRef.current?.focus()
        else confirmRef.current?.focus()
        return () => previouslyFocused?.focus?.()
    }, [stepUp])

    return (
        <div
            className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
            onMouseDown={(e) => {
                if (e.target === e.currentTarget && !pending) onClose()
            }}
        >
            <div
                ref={panelRef}
                role="dialog"
                aria-modal="true"
                aria-labelledby="confirm-money-title"
                aria-describedby={stepUp ? 'confirm-stepup-alert' : 'confirm-money-desc'}
                onKeyDown={onKeyDown}
                className="w-full max-w-sm rounded-2xl bg-card p-6 shadow-modal"
            >
                <h2 id="confirm-money-title" className="text-lg font-semibold text-foreground">
                    {t(stepUp ? 'confirm.stepUp.title' : titleKey)}
                </h2>
                <p id="confirm-money-desc" className="mt-1 text-sm leading-relaxed text-muted-foreground">
                    {t(descKey)}
                </p>

                <dl className="mt-5 space-y-2 rounded-xl bg-muted p-4 text-sm" aria-live="polite">
                    <div className="flex items-center justify-between">
                        <dt className="text-muted-foreground">{t('confirm.amount')}</dt>
                        <dd className="font-mono tabular-nums font-medium text-foreground">
                            {formatBRL(amountCents)}
                        </dd>
                    </div>

                    {isWithdraw && (
                        <div className="flex items-center justify-between">
                            <dt className="text-muted-foreground">{t('confirm.fee')}</dt>
                            <dd className="font-mono tabular-nums text-muted-foreground">{formatBRL(fee)}</dd>
                        </div>
                    )}

                    <div className="flex items-center justify-between border-t border-border pt-2">
                        <dt className="font-medium text-foreground">{t('confirm.total')}</dt>
                        <dd className="font-mono tabular-nums font-semibold text-foreground">
                            {formatBRL(totalDebit)}
                        </dd>
                    </div>

                    <div className="flex items-center justify-between">
                        <dt className="text-muted-foreground">{t('confirm.resulting')}</dt>
                        <dd className="font-mono tabular-nums text-muted-foreground">
                            {formatBRL(resultingBalance)}
                        </dd>
                    </div>
                </dl>

                {stepUp && (
                    <p
                        id="confirm-stepup-alert"
                        className="mb-3 rounded-xl bg-brand-50 p-4 text-sm leading-relaxed text-brand-800"
                        role="alert"
                    >
                        {t('confirm.stepUp.description')}
                    </p>
                )}
                <div className="mt-6 flex gap-2">
                    {stepUp ? (
                        <>
                            <Button
                                type="button"
                                variant="ghost"
                                className="flex-1"
                                onClick={onClose}
                                disabled={pending}
                            >
                                {t('common.cancel')}
                            </Button>
                            <Button
                                ref={reverifyRef}
                                type="button"
                                variant="brand"
                                className="flex-1"
                                onClick={onReverify}
                            >
                                {t('confirm.stepUp.reverify')}
                            </Button>
                        </>
                    ) : (
                        <>
                            <Button
                                type="button"
                                variant="ghost"
                                className="flex-1"
                                onClick={onClose}
                                disabled={pending}
                            >
                                {t('common.cancel')}
                            </Button>
                            <Button
                                ref={confirmRef}
                                type="button"
                                variant="brand"
                                className="flex-1"
                                onClick={onConfirm}
                                disabled={pending}
                            >
                                {pending ? t('common.loading') : t('confirm.confirm')}
                            </Button>
                        </>
                    )}
                </div>
            </div>
        </div>
    )
}
