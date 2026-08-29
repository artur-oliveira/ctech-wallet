'use client'

import {useState} from 'react'
import {useTranslation} from 'react-i18next'
import {Button} from '@/components/ui/button'
import {Dialog, DialogContent, DialogDescription, DialogTitle} from '@/components/ui/dialog'
import {formatCredits, MAX_AMOUNT_DIGITS} from '@/lib/utils/money'

interface OnboardingDialogProps {
  pending?: boolean
  error?: string
  onSubmit: (incomeValue: number) => void
  onClose: () => void
}

/**
 * Opens the user's payment (custody) subaccount.
 *
 * One field, because the provider asks for exactly one thing the wallet does
 * not already know: declared monthly income. It is a cadastral field sent
 * straight through and never persisted here, so this dialog stores nothing and
 * shows no consent copy of its own — the wallet terms already cover custody.
 */
export function OnboardingDialog({pending, error, onSubmit, onClose}: OnboardingDialogProps) {
  const {t} = useTranslation()
  const [income, setIncome] = useState(0)
  const invalid = income <= 0

  return (
    <Dialog
      open
      disablePointerDismissal={!!pending}
      onOpenChange={(open) => {
        if (!open && !pending) onClose()
      }}
    >
      <DialogContent
        render={
          <form
            noValidate
            onSubmit={(e) => {
              e.preventDefault()
              if (!invalid && !pending) onSubmit(income)
            }}
          />
        }
      >
        <DialogTitle>{t('dialog.onboarding.title')}</DialogTitle>
        <DialogDescription className="mt-1">{t('dialog.onboarding.description')}</DialogDescription>

        <label className="mt-5 block text-sm font-medium text-foreground" htmlFor="income-value">
          {t('dialog.onboarding.incomeLabel')}
        </label>
        <div className="mt-1.5 flex items-center gap-2 rounded-lg border border-border px-3 focus-within:border-brand-500 focus-within:ring-3 focus-within:ring-brand-500/20">
          <span className="text-sm text-muted-foreground">R$</span>
          <input
            id="income-value"
            autoFocus
            inputMode="decimal"
            maxLength={16}
            placeholder={t('dialog.amount.placeholder')}
            value={formatCredits(income)}
            onChange={(e) => {
              const digits = e.target.value.replace(/\D/g, '').slice(0, MAX_AMOUNT_DIGITS)
              setIncome(parseInt(digits || '0', 10))
            }}
            aria-describedby={error ? 'onboarding-error' : undefined}
            aria-invalid={!!error}
            className="h-10 w-full border-0 bg-transparent font-mono tabular-nums outline-none"
          />
        </div>
        <p className="mt-1.5 text-xs text-muted-foreground">{t('dialog.onboarding.incomeHint')}</p>

        {error && (
          <p id="onboarding-error" role="alert" className="mt-3 text-sm text-destructive">{error}</p>
        )}

        <div className="mt-6 flex flex-col-reverse gap-2 sm:flex-row">
          <Button type="button" variant="ghost" className="w-full sm:flex-1" onClick={onClose} disabled={pending}>
            {t('common.cancel')}
          </Button>
          <Button type="submit" variant="brand" className="w-full sm:flex-1" disabled={pending || invalid}>
            {pending ? t('common.loading') : t('dialog.onboarding.submit')}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  )
}
