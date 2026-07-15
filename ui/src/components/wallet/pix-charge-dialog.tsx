'use client'

import React, {useCallback, useEffect, useRef, useState} from 'react'
import {useQuery} from '@tanstack/react-query'
import {Check, Copy} from 'lucide-react'
import {useTranslation} from 'react-i18next'
import {toast} from 'sonner'
import {Button} from '@/components/ui/button'
import {formatBRL} from '@/lib/utils/money'
import {apiClient} from '@/lib/api/client'
import type {DepositResult} from '@/lib/types/api'

const POLL_DELAY_MS = 30_000 // start polling only after this — the WS path is primary
const POLL_INTERVAL_MS = 5_000

/**
 * Shown after a deposit is opened. The charge expires in 15 minutes and the
 * balance only updates after the bank confirms the payment — so the copy is
 * explicit that closing this window is safe.
 */
export function PixChargeDialog(
    {deposit, initialRealBalance, onClose, onConfirmed}: {
        deposit: DepositResult
        initialRealBalance: number
        onClose: () => void
        onConfirmed: () => void
    },
) {
    const {t} = useTranslation()
    const [copied, setCopied] = useState(false)
    const [polling, setPolling] = useState(false)
    const panelRef = useRef<HTMLDivElement>(null)

    const onKeyDown = useCallback(
        (e: React.KeyboardEvent) => {
            if (e.key === 'Escape') {
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
        [onClose],
    )

    useEffect(() => {
        const previouslyFocused = document.activeElement as HTMLElement | null
        panelRef.current?.querySelector<HTMLElement>('button')?.focus()
        return () => previouslyFocused?.focus?.()
    }, [])

    useEffect(() => {
        const timer = setTimeout(() => setPolling(true), POLL_DELAY_MS)
        return () => clearTimeout(timer)
    }, [])

    const balances = useQuery({
        queryKey: ['balances'],
        queryFn: () => apiClient.getBalances(),
        enabled: polling,
        refetchInterval: polling ? POLL_INTERVAL_MS : false,
    })

    useEffect(() => {
        const realBalance = balances.data?.real?.balance
        if (realBalance != null && realBalance >= initialRealBalance + deposit.amount) {
            toast.success(t('pix.confirmedToast'))
            onConfirmed()
        }
    }, [balances.data, initialRealBalance, deposit.amount, onConfirmed, t])

    async function copy() {
        try {
            await navigator.clipboard?.writeText(deposit.pix_copia_e_cola)
            setCopied(true)
            toast.success(t('toast.pixCopied'))
            setTimeout(() => setCopied(false), 2000)
        } catch {
            toast.error(t('common.genericError'))
        }
    }

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
                aria-labelledby="pix-charge-title"
                onKeyDown={onKeyDown}
                className="w-full max-w-sm rounded-2xl bg-card p-6 shadow-modal"
            >
                <h2 id="pix-charge-title" className="text-lg font-semibold text-foreground">
                    {t('pix.title', {amount: formatBRL(deposit.amount)})}
                </h2>
                <p className="mt-1 text-sm leading-relaxed text-muted-foreground">
                    {t('pix.description')}
                </p>

                {deposit.qr_code_base64 && (
                    // eslint-disable-next-line @next/next/no-img-element
                    <img
                        src={`data:image/png;base64,${deposit.qr_code_base64}`}
                        alt={t('pix.qrAlt')}
                        className="mx-auto mt-5 size-44 rounded-lg border border-border"
                    />
                )}

                <p className="mt-5 break-all rounded-lg bg-muted p-3 font-mono text-xs leading-relaxed text-muted-foreground">
                    {deposit.pix_copia_e_cola}
                </p>

                <Button variant="brand" className="mt-3 w-full" onClick={copy}>
                    {copied ? <Check size={16}/> : <Copy size={16}/>}
                    {copied ? t('pix.copied') : t('pix.copy')}
                </Button>

                <p className="mt-3 text-center text-xs text-muted-foreground">
                    {t('pix.validity')}
                </p>

                <Button variant="ghost" className="mt-2 w-full" onClick={onClose}>
                    {t('pix.close')}
                </Button>
            </div>
        </div>
    )
}
