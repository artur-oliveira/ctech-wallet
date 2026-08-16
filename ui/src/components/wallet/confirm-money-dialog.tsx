'use client'

import {useEffect, useRef} from 'react'
import {useTranslation} from 'react-i18next'
import {Button} from '@/components/ui/button'
import {formatBRL} from '@/lib/utils/money'
import {Dialog, DialogContent, DialogDescription, DialogTitle} from '@/components/ui/dialog'

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
 * Two-step commit for real-money moves.
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

  const totalDebit = amountCents
  const resultingBalance = Math.max(0, availableCents - totalDebit)

  const flowKey = FLOW_KEY[flow]
  const titleKey = `confirm.${flowKey}.title`
  const descKey = `confirm.${flowKey}.description`

  useEffect(() => {
    if (stepUp) reverifyRef.current?.focus()
  }, [stepUp])

  return (
    <Dialog
      open
      disablePointerDismissal={!!pending}
      onOpenChange={(open) => {
        if (!open && !pending) onClose()
      }}
    >
      <DialogContent initialFocus={stepUp ? reverifyRef : confirmRef}>
        <DialogTitle>
          {t(stepUp ? 'confirm.stepUp.title' : titleKey)}
        </DialogTitle>
        <DialogDescription className="mt-1">
          {t(descKey)}
        </DialogDescription>

        <dl className="mt-5 space-y-2 rounded-xl bg-muted p-4 text-sm" aria-live="polite">
          <div className="flex items-center justify-between">
            <dt className="text-muted-foreground">{t('confirm.amount')}</dt>
            <dd className="font-mono tabular-nums font-medium text-foreground">
              {formatBRL(amountCents)}
            </dd>
          </div>

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
        <div className="mt-6 flex flex-col-reverse gap-2 sm:flex-row">
          {stepUp ? (
            <>
              <Button
                type="button"
                variant="ghost"
                className="w-full sm:flex-1"
                onClick={onClose}
                disabled={pending}
              >
                {t('common.cancel')}
              </Button>
              <Button
                ref={reverifyRef}
                type="button"
                variant="brand"
                className="w-full sm:flex-1"
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
                className="w-full sm:flex-1"
                onClick={onClose}
                disabled={pending}
              >
                {t('common.cancel')}
              </Button>
              <Button
                ref={confirmRef}
                type="button"
                variant="brand"
                className="w-full sm:flex-1"
                onClick={onConfirm}
                disabled={pending}
              >
                {pending ? t('common.loading') : t('confirm.confirm')}
              </Button>
            </>
          )}
        </div>
      </DialogContent>
    </Dialog>
  )
}
