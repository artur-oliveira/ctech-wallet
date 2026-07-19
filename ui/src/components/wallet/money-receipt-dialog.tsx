'use client'

import {useRef} from 'react'
import {CheckCircle2} from 'lucide-react'
import {useTranslation} from 'react-i18next'
import {Button} from '@/components/ui/button'
import {Dialog, DialogContent, DialogTitle} from '@/components/ui/dialog'

/**
 * Brief peak-end confirmation for a completed real-money move — replaces a
 * vanishing toast as the primary "it worked" signal for withdrawals and
 * ring-fence transfers (CLAUDE.md/critique: money-move peak-end is toast-only).
 */
export function MoneyReceiptDialog({title, amountLabel, details, onClose}: {
    title: string
    amountLabel: string
    details?: Array<{label: string; value: string}>
    onClose: () => void
}) {
    const {t} = useTranslation()
    const closeRef = useRef<HTMLButtonElement>(null)

    return (
        <Dialog
            open
            onOpenChange={(open) => {
                if (!open) onClose()
            }}
        >
            <DialogContent initialFocus={closeRef} className="text-center">
                <div className="mx-auto flex size-12 items-center justify-center rounded-full bg-brand-50 text-brand-600">
                    <CheckCircle2 aria-hidden="true" size={28}/>
                </div>
                <DialogTitle className="mt-4">
                    {title}
                </DialogTitle>
                <p className="mt-1 font-mono text-2xl font-bold tabular-nums text-foreground">
                    {amountLabel}
                </p>
                {details && details.length > 0 && (
                    <dl className="mt-5 space-y-2 rounded-xl bg-muted p-4 text-sm">
                        {details.map((detail) => (
                            <div key={detail.label} className="flex items-center justify-between gap-4">
                                <dt className="text-muted-foreground">{detail.label}</dt>
                                <dd className="font-mono tabular-nums text-foreground">{detail.value}</dd>
                            </div>
                        ))}
                    </dl>
                )}
                <Button ref={closeRef} variant="brand" className="mt-6 w-full" onClick={onClose}>
                    {t('common.close')}
                </Button>
            </DialogContent>
        </Dialog>
    )
}
