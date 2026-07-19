'use client'

import React, {useEffect, useRef, useState} from 'react'
import {Check, Copy} from 'lucide-react'
import {useTranslation} from 'react-i18next'
import {toast} from 'sonner'
import {Button} from '@/components/ui/button'
import {formatBRL} from '@/lib/utils/money'
import type {DepositResult} from '@/lib/types/api'
import {Dialog, DialogContent, DialogDescription, DialogTitle} from '@/components/ui/dialog'

const TICK_MS = 1_000

/** mm:ss, floored — never negative. */
function formatCountdown(remainingSec: number): string {
    const s = Math.max(0, Math.floor(remainingSec))
    const mm = Math.floor(s / 60)
    const ss = s % 60
    return `${mm}:${ss.toString().padStart(2, '0')}`
}

/**
 * Shown after a deposit is opened. The charge expires at `deposit.expires_at`
 * (server-computed) and the balance only updates after the bank confirms the
 * payment — so the copy is explicit that closing this window is safe as long
 * as the code hasn't expired yet.
 */
export function PixChargeDialog(
    {deposit, onClose}: {
        deposit: DepositResult
        onClose: () => void
    },
) {
    const {t} = useTranslation()
    const [copied, setCopied] = useState(false)
    const [remainingSec, setRemainingSec] = useState(() => deposit.expires_at - Date.now() / 1000)
    const copyRef = useRef<HTMLButtonElement>(null)
    const expired = remainingSec <= 0

    // Ticks every second while the code is still valid; stops once expired —
    // nothing left to count down, and no point waking the tab up forever.
    useEffect(() => {
        if (expired) return
        const tick = setInterval(() => {
            setRemainingSec(deposit.expires_at - Date.now() / 1000)
        }, TICK_MS)
        return () => clearInterval(tick)
    }, [expired, deposit.expires_at])

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
        <Dialog
            open
            onOpenChange={(open) => {
                if (!open) onClose()
            }}
        >
            <DialogContent initialFocus={copyRef}>
                <DialogTitle>
                    {t('pix.title', {amount: formatBRL(deposit.amount)})}
                </DialogTitle>
                <DialogDescription className="mt-1">
                    {t('pix.description')}
                </DialogDescription>

                {expired ? (
                    <p role="status" className="mt-5 rounded-lg border border-destructive/30 bg-destructive/10 p-3 text-center text-sm text-destructive">
                        {t('pix.expired')}
                    </p>
                ) : (
                    <>
                        {deposit.qr_code_base64 && (
                            // eslint-disable-next-line @next/next/no-img-element
                            <img
                                src={`data:image/png;base64,${deposit.qr_code_base64}`}
                                alt={t('pix.qrAlt')}
                                className="mx-auto mt-5 aspect-square h-auto w-44 max-w-full rounded-lg border border-border"
                            />
                        )}

                        <p className="mt-5 break-all rounded-lg bg-muted p-3 font-mono text-xs leading-relaxed text-muted-foreground">
                            {deposit.pix_copia_e_cola}
                        </p>

                        <Button ref={copyRef} variant="brand" className="mt-3 w-full" onClick={copy}>
                            {copied ? <Check size={16}/> : <Copy size={16}/>}
                            {copied ? t('pix.copied') : t('pix.copy')}
                        </Button>

                        <p className="mt-3 text-center text-xs tabular-nums text-muted-foreground">
                            {t('pix.expiresIn', {time: formatCountdown(remainingSec)})}
                        </p>
                    </>
                )}

                <Button variant="ghost" className="mt-2 w-full" onClick={onClose}>
                    {t('pix.close')}
                </Button>
            </DialogContent>
        </Dialog>
    )
}
