'use client'

import {useMemo} from 'react'
import {Controller, useForm} from 'react-hook-form'
import {zodResolver} from '@hookform/resolvers/zod'
import {z} from 'zod'
import {useTranslation} from 'react-i18next'
import {Button} from '@/components/ui/button'
import {
  formatBRL,
  formatCredits,
  MAX_AMOUNT_CENTS,
  MAX_AMOUNT_DIGITS
} from '@/lib/utils/money'
import {Dialog, DialogContent, DialogDescription, DialogTitle} from '@/components/ui/dialog'
import {amountBounds} from '@/lib/utils/amount-bounds'

type Flow = 'deposit' | 'withdraw'

const FLOW_KEY: Record<Flow, 'deposit' | 'withdraw'> = {
  deposit: 'deposit',
  withdraw: 'withdraw',
}

interface AmountDialogProps {
  flow: Flow
  /** Caps the amount at the available balance (withdraw). */
  maxCents?: number
  /** Server-reported minimum for this flow (deposit/withdraw). */
  minCents?: number
  /** Server-computed "allowed right now" (daily limits folded in); 0 blocks the flow. */
  limitCents?: number
  pending?: boolean
  onSubmit?: (amount: number) => void
  /** When set, replaces the mutation: the amount is handed to a confirm step instead of committing. */
  onProceed?: (amount: number) => void
  onClose: () => void
}

/** Shared amount entry used by deposit and withdrawal. */
export function AmountDialog({
                               flow,
                               maxCents,
                               minCents,
                               limitCents,
                               pending,
                               onSubmit,
                               onProceed,
                               onClose
                             }: AmountDialogProps) {
  const {t} = useTranslation()
  const flowKey = FLOW_KEY[flow]

  // The R$ 1.000.000 ceiling applies only to money coming in (a PIX deposit). A withdrawal is capped
  // only by the current balance (maxCents), never by the million cap.
  const capMillion = flow === 'deposit'
  const balanceCap = maxCents ?? Number.POSITIVE_INFINITY
  const millionCap = capMillion ? MAX_AMOUNT_CENTS : Number.POSITIVE_INFINITY
  const bounds = amountBounds({ceiling: millionCap, balance: balanceCap, limitNow: limitCents, min: minCents})
  const effectiveMax = bounds.max
  const hasServerLimit = limitCents != null
  const fmt = formatBRL

  const schema = useMemo(() => {
    const overMsg =
      hasServerLimit
        ? t('dialog.error.maxExceeded', {max: formatBRL(effectiveMax)})
        : flow === 'withdraw'
        ? t('dialog.error.overWithdrawable', {amount: formatBRL(effectiveMax)})
        : balanceCap <= millionCap && maxCents != null
          ? t('dialog.error.overBalance', {amount: fmt(maxCents)})
          : t('dialog.error.maxExceeded', {max: formatBRL(MAX_AMOUNT_CENTS)})

    const amount = z
      .number({error: t('dialog.error.invalid')})
      .int()
      .positive(t('dialog.error.required'))
      .min(bounds.min, t('dialog.error.belowMin', {min: formatBRL(bounds.min)}))
      .max(effectiveMax, overMsg)

    return z.object({amount})
  }, [effectiveMax, bounds.min, hasServerLimit, flow, balanceCap, millionCap, maxCents, t, fmt])

  const {
    control,
    handleSubmit,
    formState: {errors},
  } = useForm<{ amount: number }>({
    resolver: zodResolver(schema),
    defaultValues: {amount: 0},
  })

  const submit = handleSubmit((data) => {
    if (onProceed) {
      onProceed(data.amount)
    } else if (onSubmit) {
      onSubmit(data.amount)
    }
  })

  return (
    <Dialog
      open
      disablePointerDismissal={!!pending}
      onOpenChange={(open) => {
        if (!open && !pending) onClose()
      }}
    >
      <DialogContent render={<form onSubmit={submit} noValidate/>}>
        <DialogTitle>{t(`dialog.${flowKey}.title`)}</DialogTitle>
        <DialogDescription className="mt-1">{t(`dialog.${flowKey}.description`)}</DialogDescription>

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
                    className="inline-flex items-center justify-center text-xs font-semibold text-brand-600 hover:text-brand-700 hover:underline [@media(pointer:coarse)]:min-h-11 [@media(pointer:coarse)]:min-w-11"
                  >
                    {t('dialog.amount.maxButton', {max: fmt(effectiveMax)})}
                  </button>
                </div>
              )}
            </>
          )}
        />

        {hasServerLimit && !bounds.blocked && (
          <p className="mt-1.5 text-xs text-muted-foreground">
            {t('dialog.range', {min: formatBRL(bounds.min), max: formatBRL(effectiveMax)})}
          </p>
        )}
        {hasServerLimit && bounds.blocked && (
          <p role="status" className="mt-1.5 text-sm text-destructive">{t('dialog.limitReached')}</p>
        )}
        {capMillion && !hasServerLimit && (
          <p className="mt-1.5 text-xs text-muted-foreground">{t('dialog.max', {max: formatBRL(MAX_AMOUNT_CENTS)})}</p>
        )}
        {maxCents != null && (
          <p className="mt-1.5 text-xs text-muted-foreground">{t('dialog.available', {amount: fmt(maxCents)})}</p>
        )}

        {errors.amount && (
          <p id="amount-error" role="alert" className="mt-1.5 text-sm text-destructive">
            {errors.amount.message}
          </p>
        )}


        <div className="mt-6 flex flex-col-reverse gap-2 sm:flex-row">
          <Button type="button" variant="ghost" className="w-full sm:flex-1" onClick={onClose} disabled={pending}>
            {t('common.cancel')}
          </Button>
          <Button type="submit" variant="brand" className="w-full sm:flex-1" disabled={pending || bounds.blocked}>
            {pending ? t('common.loading') : t(`dialog.${flowKey}.submit`)}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  )
}
