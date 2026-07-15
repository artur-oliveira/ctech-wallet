'use client'

import {useCallback, useEffect, useMemo, useRef} from 'react'
import {Controller, useForm, useWatch} from 'react-hook-form'
import {zodResolver} from '@hookform/resolvers/zod'
import {z} from 'zod'
import {useTranslation} from 'react-i18next'
import {Button} from '@/components/ui/button'
import {formatBRL, formatCredits, MAX_AMOUNT_CENTS, MAX_AMOUNT_DIGITS} from '@/lib/utils/money'
import {withdrawalFee} from '@/lib/utils/fee'

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
    pending?: boolean
    onSubmit?: (amount: number, pixKey?: string) => void
    /** When set, replaces the mutation: the amount (and PIX key) are handed to a confirm step instead of committing. */
    onProceed?: (amount: number, pixKey?: string) => void
    onClose: () => void
}

/** Shared amount entry used by deposit, withdrawal, and credit purchase. */
export function AmountDialog({flow, maxCents, pending, onSubmit, onProceed, onClose}: AmountDialogProps) {
    const {t} = useTranslation()
    const withPixKey = flow === 'withdraw'
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
            ? Math.max(0, maxCents - withdrawalFee(maxCents))
            : balanceCap
    const effectiveMax = Math.min(feeAwareCap, millionCap)
    // Sandbox credits carry no currency symbol (contract + invariant #7) — every
    // amount shown to the user in the credits flow must go through formatCredits.
    const fmt = flow === 'credits' ? formatCredits : formatBRL

    const schema = useMemo(() => {
        const overMsg =
            balanceCap <= millionCap && maxCents != null
                ? t('dialog.error.overBalance', {amount: fmt(maxCents)})
                : t('dialog.error.maxExceeded', {max: formatBRL(MAX_AMOUNT_CENTS)})

        const amount = z
            .number({error: t('dialog.error.invalid')})
            .int()
            .positive(t('dialog.error.required'))
            .max(effectiveMax, overMsg)

        const pixKey = withPixKey
            ? z.string().trim().min(1, t('dialog.error.pixKeyRequired')).max(100, t('dialog.error.pixKeyTooLong'))
            : z.string().max(100)

        return z.object({amount, pixKey})
    }, [effectiveMax, balanceCap, millionCap, maxCents, withPixKey, t, fmt])

    const {
        control,
        register,
        handleSubmit,
        formState: {errors},
    } = useForm<{ amount: number; pixKey: string }>({
        resolver: zodResolver(schema),
        defaultValues: {amount: 0, pixKey: ''},
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
        const pixKey = withPixKey ? data.pixKey.trim() : undefined
        if (onProceed) {
            onProceed(data.amount, pixKey)
        } else if (onSubmit) {
            onSubmit(data.amount, pixKey)
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
                                {flow !== 'credits' && (
                                    <span className="text-sm text-muted-foreground">R$</span>
                                )}
                                <input
                                    id="amount"
                                    autoFocus
                                    inputMode="decimal"
                                    maxLength={16}
                                    placeholder={t('dialog.amount.placeholder')}
                                    value={formatCredits(field.value ?? 0)}
                                    onChange={(e) => {
                                        // 9 digits caps typing at R$ 1.000.000 when the million cap
                                        // applies; without it, allow more and let effectiveMax clamp.
                                        const maxDigits = capMillion ? MAX_AMOUNT_DIGITS : 12
                                        const digits = e.target.value.replace(/\D/g, '').slice(0, maxDigits)
                                        let cents = parseInt(digits || '0', 10)
                                        if (cents > effectiveMax) cents = effectiveMax
                                        field.onChange(cents)
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
                        </>
                    )}
                />

                {capMillion && (
                    <p className="mt-1.5 text-xs text-muted-foreground">{t('dialog.max', {max: formatBRL(MAX_AMOUNT_CENTS)})}</p>
                )}
                {maxCents != null && (
                    <p className="mt-1.5 text-xs text-muted-foreground">{t('dialog.available', {amount: fmt(maxCents)})}</p>
                )}

                {withPixKey && amount > 0 && (
                    <p className="mt-1.5 text-xs text-muted-foreground">
                        {t('dialog.amount.feePreview', {fee: formatBRL(withdrawalFee(amount))})}
                    </p>
                )}

                {errors.amount && (
                    <p id="amount-error" role="alert" className="mt-1.5 text-sm text-destructive">
                        {errors.amount.message}
                    </p>
                )}

                {withPixKey && (
                    <>
                        <label className="mt-4 block text-sm font-medium text-foreground" htmlFor="pixkey">
                            {t('dialog.pixKey.label')}
                        </label>
                        <input
                            id="pixkey"
                            maxLength={100}
                            placeholder={t('dialog.pixKey.placeholder')}
                            aria-invalid={!!errors.pixKey}
                            aria-describedby={errors.pixKey ? 'pixkey-error' : undefined}
                            className={`mt-1.5 h-10 w-full rounded-lg border px-3 text-sm outline-none focus:ring-3 ${
                                errors.pixKey
                                    ? 'border-destructive focus:border-destructive focus:ring-destructive/20'
                                    : 'border-border focus:border-brand-500 focus:ring-brand-500/20'
                            }`}
                            {...register('pixKey')}
                        />
                        <p className="mt-1.5 text-xs text-muted-foreground">{t('dialog.pixKey.hint')}</p>
                        {errors.pixKey && (
                            <p id="pixkey-error" role="alert" className="mt-1.5 text-sm text-destructive">
                                {errors.pixKey.message}
                            </p>
                        )}
                    </>
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
