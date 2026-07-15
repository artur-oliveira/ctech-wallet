'use client'

import {useState} from 'react'
import {useRouter} from 'next/navigation'
import {useMutation, useQueryClient} from '@tanstack/react-query'
import {toast} from 'sonner'
import {Dice5} from 'lucide-react'
import {useTranslation} from 'react-i18next'
import {apiClient, ApiError} from '@/lib/api/client'
import {ProtectedRoute} from '@/components/protected-route'
import {Button} from '@/components/ui/button'

function activationMessage(err: unknown, t: (k: string) => string): string {
    if (!(err instanceof ApiError)) return t('gambling.error.generic')
    switch (err.type) {
        case '/problems/kyc-not-verified':
            return t('gambling.error.kycNotVerified')
        case '/problems/gambling-terms-required':
            return t('gambling.error.termsRequired')
        default:
            return err.detail || t('gambling.error.generic')
    }
}

/**
 * Opt-in to the game + sandbox wallets.
 *
 * The checkbox starts unchecked and the button stays disabled until it is ticked:
 * a pre-ticked box is not consent, and this is the consent the whole ring-fence
 * rests on. Activation is recorded in an append-only audit log.
 */
function ActivateGamblingInner() {
    const {t} = useTranslation()
    const router = useRouter()
    const qc = useQueryClient()
    const [checked, setChecked] = useState(false)

    const activate = useMutation({
        mutationFn: () => apiClient.activateGambling(),
        onSuccess: () => {
            void qc.invalidateQueries({queryKey: ['balances']})
            toast.success(t('toast.gamblingActivated'))
            router.push('/dashboard')
        },
        onError: (err) => toast.error(activationMessage(err, t)),
    })

    return (
        <div className="flex min-h-screen items-center justify-center bg-background px-4">
            <div className="w-full max-w-md space-y-5 rounded-2xl border border-brand-100 bg-card p-6 shadow-card">
                <div className="flex size-10 items-center justify-center rounded-lg bg-brand-600 text-white">
                    <Dice5 size={20}/>
                </div>

                <div>
                    <h1 className="text-lg font-semibold text-foreground">{t('gambling.title')}</h1>
                    <p className="mt-1 text-sm leading-relaxed text-muted-foreground">
                        {t('gambling.description')}
                    </p>
                </div>

                <ul className="space-y-2 rounded-xl bg-muted p-4 text-sm leading-relaxed text-muted-foreground">
                    <li>{t('gambling.bullet1')}</li>
                    <li>{t('gambling.bullet2')}</li>
                    <li>{t('gambling.bullet3')}</li>
                </ul>

                <label className="flex items-start gap-2 text-sm text-muted-foreground">
                    <input
                        type="checkbox"
                        checked={checked}
                        onChange={(e) => setChecked(e.target.checked)}
                        className="mt-0.5 size-4 shrink-0 rounded border-border accent-brand-600"
                    />
                    <span>
            {t('gambling.checkboxPrefix')}{' '}
                        <a
                            href="/gambling-addendum"
                            target="_blank"
                            rel="noreferrer"
                            className="text-foreground underline underline-offset-4"
                        >
              {t('gambling.termsLink')}
            </a>
            .
          </span>
                </label>

                <div className="flex gap-2">
                    <Button variant="outline" className="flex-1" onClick={() => router.push('/dashboard')}>
                        {t('gambling.later')}
                    </Button>
                    <Button
                        variant="brand"
                        className="flex-1"
                        disabled={!checked || activate.isPending}
                        onClick={() => activate.mutate()}
                    >
                        {activate.isPending ? t('gambling.activating') : t('gambling.activate')}
                    </Button>
                </div>
            </div>
        </div>
    )
}

export default function ActivateGamblingPage() {
    return (
        <ProtectedRoute>
            <ActivateGamblingInner/>
        </ProtectedRoute>
    )
}
