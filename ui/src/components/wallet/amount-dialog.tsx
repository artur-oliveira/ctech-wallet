'use client'

import {useCallback, useEffect, useMemo, useRef} from 'react'
import {Controller, useForm, useWatch} from 'react-hook-form'
import {zodResolver} from '@hookform/resolvers/zod'
import {z} from 'zod'
import {useTranslation} from 'react-i18next'
import {Button} from '@/components/ui/button'
import {formatBRL, formatCredits, formatCreditsAmount, MAX_AMOUNT_CENTS, MAX_AMOUNT_DIGITS, toCredits} from '@/lib/utils/money'
import {maxWithdrawable, withdrawalFee, type WithdrawalFeeConfig} from '@/lib/utils/fee'

type Flow = 'deposit' | 'withdraw' | 'credits' | 'fund-game' | 'return-game'

const FLOW_KEY: Record<Flow, 'deposit' | 'withdraw' | 'credits' | 'fundGame' | 'returnGame'> = {
    deposit: 'deposit',
    withdraw: 'withdraw',
    credits: 'credits',
    'fund-game': 'fundGame',
    'return-game': 'returnGame',
}

interface AmountDialogProps {
    flow: Flow
    /** Caps the amount at the available balance (withdraw, fund-game, credits, return-game). */
    maxCents?: number
    /** Effective fee fields from the real wallet; used only by the withdrawal flow. */
    feeConfig?: WithdrawalFeeConfig
    pending?: boolean
    onSubmit?: (amount: number) => void
    /** When set, replaces the mutation: the amount is handed to a confirm step instead of committing. */
    onProceed?: (amount: number) => void
    onClose: () => void
}

/** Shared amount entry used by deposit, withdrawal, and credit purchase. */
export function AmountDialog({flow, maxCents, feeConfig, pending, onSubmit, onProceed, onClose}: AmountDialogProps) {
    const {t} = useTranslation()
    const flowKey = FLOW_KEY[flow]

    // The R$ 1.000.000 ceiling applies ONLY to money entering the game ring-fence:
    // a PIX deposit and a real → game transfer. Withdrawals and returns out of the
    // ring-fence are capped only by the current balance (maxCents) — never by the
    // million cap. See CLAUDE.md invariant 7.
    const capMillion = flow === 'deposit' || flow === 'fund-game'
    const balanceCap = maxCents ?? Number.POSITIVE_INFINITY
    const millionCap = capMillion ? MAX_AMOUNT_CENTS : Number.POSITIVE_INFINITY
    // A withdrawal also pays the fee, so the largest amount that can leave the
    // wallet without tripping insufficient-balance is balance − fee. Credits,
    // returns, and real↔game transfers carry no fee, so their cap is raw balance.
    const feeAwareCap =
        flow === 'withdraw' && maxCents != null
            ? maxWithdrawable(maxCents, feeConfig)
            : balanceCap
    const effectiveMax = Math.min(feeAwareCap, millionCap)
    // Sandbox credits carry no currency symbol (contract + invariant #7) — every
    // amount shown to the user in the credits flow must go through formatCredits.
    const fmt = flow === 'credits' ? formatCredits : formatBRL

    const schema = useMemo(() => {
        const overMsg =
            flow === 'withdraw'
                ? t('dialog.error.overWithdrawable', {amount: formatBRL(effectiveMax)})
                : balanceCap <= millionCap && maxCents != null
                ? t('dialog.error.overBalance', {amount: fmt(maxCents)})
                : t('dialog.error.maxExceeded', {max: formatBRL(MAX_AMOUNT_CENTS)})

        const amount = z
            .number({error: t('dialog.error.invalid')})
            .int()
            .positive(t('dialog.error.required'))
            .max(effectiveMax, overMsg)

        return z.object({amount})
    }, [effectiveMax, flow, balanceCap, millionCap, maxCents, t, fmt])

    const {
        control,
        handleSubmit,
        formState: {errors},
    } = useForm<{ amount: number }>({
        resolver: zodResolver(schema),
        defaultValues: {amount: 0},
    })

    const amount = useWatch({control, name: 'amount'}) ?? 0

    const panelRef = useRef<HTMLFormElement>(null)

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
        panelRef.current?.querySelector<HTMLElement>('input, button')?.focus()
        return () => previouslyFocused?.focus?.()
    }, [])

    const submit = handleSubmit((data) => {
        if (onProceed) {
            onProceed(data.amount)
        } else if (onSubmit) {
            onSubmit(data.amount)
        }
    })

    return (
        <div
            className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
            onMouseDown={(e) => {
                if (e.target === e.currentTarget && !pending) onClose()
            }}
        >
            <form
                onSubmit={submit}
                noValidate
                role="dialog"
                aria-modal="true"
                aria-labelledby="amount-dialog-title"
                onKeyDown={onKeyDown}
                ref={panelRef}
                className="w-full max-w-sm rounded-2xl bg-card p-6 shadow-modal"
            >
                <h2 id="amount-dialog-title"
                    className="text-lg font-semibold text-foreground">{t(`dialog.${flowKey}.title`)}</h2>
                <p className="mt-1 text-sm leading-relaxed text-muted-foreground">{t(`dialog.${flowKey}.description`)}</p>

                <label className="mt-5 block text-sm font-medium text-foreground" htmlFor="amount">
                    {t('dialog.amount.label')}
                </label>
                <Controller
                    name="amount"
                    control={control}
                    render={({field}) => (
                        <>
                            <div
                                className={`mt-1.5 flex items-center gap-2 rounded-lg border px-3 focus-within:ring-3 ${
                                    errors.amount
                                        ? 'border-destructive focus-within:border-destructive focus-within:ring-destructive/20'
                                        : 'border-border focus-within:border-brand-500 focus-within:ring-brand-500/20'
                                }`}
                            >
                                <span className="text-sm text-muted-foreground">R$</span>
                                <input
                                    id="amount"
                                    autoFocus
                                    inputMode="decimal"
                                    maxLength={16}
                                    placeholder={t('dialog.amount.placeholder')}
                                    value={formatCredits(field.value ?? 0)}
                                    onChange={(e) => {
                                        // 9 digits caps typing at R$ 1.000.000 when the million cap
                                        // applies; otherwise allow more and let the schema explain the limit.
                                        const maxDigits = capMillion ? MAX_AMOUNT_DIGITS : 12
                                        const digits = e.target.value.replace(/\D/g, '').slice(0, maxDigits)
                                        field.onChange(parseInt(digits || '0', 10))
                                    }}
                                    onBlur={field.onBlur}
                                    aria-invalid={!!errors.amount}
                                    aria-describedby={errors.amount ? 'amount-error' : undefined}
                                    className="h-10 w-full border-0 bg-transparent font-mono tabular-nums outline-none"
                                />
                            </div>
                            {maxCents != null && (
                                <div className="mt-1.5 flex justify-end">
                                    <button
                                        type="button"
                                        onClick={() => field.onChange(effectiveMax)}
                                        className="text-xs font-semibold text-brand-600 hover:text-brand-700 hover:underline"
                                    >
                                        {t('dialog.amount.maxButton', {max: fmt(effectiveMax)})}
                                    </button>
                                </div>
                            )}
                            {flow === 'credits' && amount > 0 && (
                                <p className="mt-1.5 text-xs text-muted-foreground">
                                    {t('dialog.amount.creditsPreview', {credits: formatCreditsAmount(toCredits(amount))})}
                                </p>
                            )}
                        </>
                    )}
                />

                {capMillion && (
                    <p className="mt-1.5 text-xs text-muted-foreground">{t('dialog.max', {max: formatBRL(MAX_AMOUNT_CENTS)})}</p>
                )}
                {maxCents != null && (
                    <p className="mt-1.5 text-xs text-muted-foreground">{t('dialog.available', {amount: fmt(maxCents)})}</p>
                )}

                {flow === 'withdraw' && amount > 0 && (
                    <p className="mt-1.5 text-xs text-muted-foreground">
                        {t('dialog.amount.feePreview', {fee: formatBRL(withdrawalFee(amount, feeConfig))})}
                    </p>
                )}

                {errors.amount && (
                    <p id="amount-error" role="alert" className="mt-1.5 text-sm text-destructive">
                        {errors.amount.message}
                    </p>
                )}

                {flow === 'withdraw' && (
                    <p className="mt-4 rounded-lg bg-muted p-3 text-xs leading-relaxed text-muted-foreground">
                        {t('dialog.withdraw.pixDestination')}
                    </p>
                )}

                <div className="mt-6 flex gap-2">
                    <Button type="button" variant="ghost" className="flex-1" onClick={onClose} disabled={pending}>
                        {t('common.cancel')}
                    </Button>
                    <Button type="submit" variant="brand" className="flex-1" disabled={pending}>
                        {pending ? t('common.loading') : t(`dialog.${flowKey}.submit`)}
                    </Button>
                </div>
            </form>
        </div>
    )
}
