'use client'

import {useCallback, useEffect, useRef} from 'react'
import {CheckCircle2} from 'lucide-react'
import {useTranslation} from 'react-i18next'
import {Button} from '@/components/ui/button'

/**
 * Brief peak-end confirmation for a completed real-money move — replaces a
 * vanishing toast as the primary "it worked" signal for withdrawals and
 * ring-fence transfers (CLAUDE.md/critique: money-move peak-end is toast-only).
 */
export function MoneyReceiptDialog({title, amountLabel, onClose}: {
    title: string
    amountLabel: string
    onClose: () => void
}) {
    const {t} = useTranslation()
    const panelRef = useRef<HTMLDivElement>(null)

    const onKeyDown = useCallback(
        (e: React.KeyboardEvent) => {
            if (e.key === 'Escape') {
                e.preventDefault()
                onClose()
            }
        },
        [onClose],
    )

    useEffect(() => {
        const previouslyFocused = document.activeElement as HTMLElement | null
        panelRef.current?.querySelector<HTMLElement>('button')?.focus()
        return () => previouslyFocused?.focus?.()
    }, [])

    return (
        <div
            className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
            onMouseDown={(e) => {
                if (e.target === e.currentTarget) onClose()
            }}
        >
            <div
                ref={panelRef}
                role="dialog"
                aria-modal="true"
                aria-labelledby="money-receipt-title"
                onKeyDown={onKeyDown}
                className="w-full max-w-sm rounded-2xl bg-card p-6 text-center shadow-modal"
            >
                <div className="mx-auto flex size-12 items-center justify-center rounded-full bg-brand-50 text-brand-600">
                    <CheckCircle2 size={28}/>
                </div>
                <h2 id="money-receipt-title" className="mt-4 text-lg font-semibold text-foreground">
                    {title}
                </h2>
                <p className="mt-1 font-mono text-2xl font-bold tabular-nums text-foreground">
                    {amountLabel}
                </p>
                <Button variant="brand" className="mt-6 w-full" onClick={onClose}>
                    {t('common.close')}
                </Button>
            </div>
        </div>
    )
}
