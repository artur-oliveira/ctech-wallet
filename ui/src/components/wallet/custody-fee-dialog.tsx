'use client'

import {useState} from 'react'
import {Check, Copy} from 'lucide-react'
import {useTranslation} from 'react-i18next'
import {toast} from 'sonner'
import Image from 'next/image'
import {Button} from '@/components/ui/button'
import {Dialog, DialogContent, DialogDescription, DialogTitle} from '@/components/ui/dialog'
import {formatBRL} from '@/lib/utils/money'
import type {OnboardingFee} from '@/lib/types/api'

/**
 * The one-off verification fee that opens a payment account.
 *
 * Deliberately not the deposit charge dialog: this money is a purchase, not a
 * deposit — it never lands in the user's balance, and the provider consumes it
 * when the account is created, so it does not come back if the registration is
 * later refused. Both facts are stated here, before the user pays, rather than
 * left to the terms.
 *
 * No countdown either: the charge outlives a session on purpose, because this
 * is an onboarding step someone may well leave and come back to.
 */
export function CustodyFeeDialog({fee, onClose}: { fee: OnboardingFee; onClose: () => void }) {
  const {t} = useTranslation()
  const [copied, setCopied] = useState(false)

  async function copy() {
    try {
      await navigator.clipboard?.writeText(fee.qr_code)
      setCopied(true)
      toast.success(t('toast.pixCopied'))
      setTimeout(() => setCopied(false), 2000)
    } catch {
      toast.error(t('common.genericError'))
    }
  }

  return (
    <Dialog open onOpenChange={(open) => {
      if (!open) onClose()
    }}>
      <DialogContent>
        <DialogTitle>{t('dialog.custodyFee.title')}</DialogTitle>
        <DialogDescription className="mt-1">{t('dialog.custodyFee.description')}</DialogDescription>

        <p className="mt-4 text-center font-mono text-2xl tabular-nums">{formatBRL(fee.amount)}</p>

        {fee.qr_code_base64 && (
          <div className="mt-4 flex justify-center">
            <Image
              src={`data:image/png;base64,${fee.qr_code_base64}`}
              alt={t('dialog.custodyFee.qrAlt')}
              width={200}
              height={200}
              unoptimized
              className="rounded-lg border border-border"
            />
          </div>
        )}

        <p className="mt-4 rounded-lg bg-muted px-3 py-2 font-mono text-xs break-all">{fee.qr_code}</p>

        <p className="mt-3 text-sm text-muted-foreground">{t('dialog.custodyFee.nonRefundable')}</p>

        <div className="mt-6 flex flex-col-reverse gap-2 sm:flex-row">
          <Button type="button" variant="ghost" className="w-full sm:flex-1" onClick={onClose}>
            {t('common.close')}
          </Button>
          <Button type="button" variant="brand" className="w-full sm:flex-1" onClick={copy}>
            {copied ? <Check size={16}/> : <Copy size={16}/>}
            {t('dialog.custodyFee.copy')}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  )
}
