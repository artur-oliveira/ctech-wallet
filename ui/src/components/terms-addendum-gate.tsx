'use client'

import {useState} from 'react'
import {useMutation, useQueryClient} from '@tanstack/react-query'
import {ShieldCheck} from 'lucide-react'
import {useTranslation} from 'react-i18next'
import {apiClient} from '@/lib/api/client'
import {Button} from '@/components/ui/button'

/**
 * Blocks the whole app until the user accepts the wallet's terms addendum.
 *
 * The wallet custodies real money and holds a non-convertible sandbox balance —
 * neither is covered by the account's master terms, and an SSO sign-up never
 * presents a checkbox for product-specific terms. Acceptance is checked against
 * the current version, so bumping it re-gates everyone.
 */
export function TermsAddendumGate() {
    const {t} = useTranslation()
    const qc = useQueryClient()
    const [checked, setChecked] = useState(false)

    const accept = useMutation({
        mutationFn: () => apiClient.acceptTermsAddendum(),
        onSuccess: () => qc.invalidateQueries({queryKey: ['me']}),
    })

    return (
        <div className="flex min-h-screen items-center justify-center bg-background px-4">
            <div className="w-full max-w-md space-y-5 rounded-2xl border border-brand-100 bg-card p-6">
                <div className="flex size-10 items-center justify-center rounded-lg bg-brand-600 text-white">
                    <ShieldCheck size={20}/>
                </div>

                <div>
                    <h1 className="text-lg font-semibold text-foreground">{t('terms.title')}</h1>
                    <p className="mt-1 text-sm leading-relaxed text-muted-foreground">
                        {t('terms.description')}
                    </p>
                </div>

                {accept.isError && (
                    <p className="text-sm text-red-600">{t('terms.error')}</p>
                )}

                <label className="flex items-start gap-2 text-sm text-muted-foreground">
                    <input
                        type="checkbox"
                        checked={checked}
                        onChange={(e) => setChecked(e.target.checked)}
                        className="mt-0.5 size-4 shrink-0 rounded border-border accent-brand-600"
                    />
                    <span>
            {t('terms.checkboxPrefix')}{' '}
                        <a
                            href="/terms-addendum"
                            target="_blank"
                            rel="noreferrer"
                            className="text-foreground underline underline-offset-4"
                        >
              {t('terms.termsLink')}
            </a>
            .
          </span>
                </label>

                <Button
                    variant="brand"
                    className="w-full"
                    disabled={!checked || accept.isPending}
                    onClick={() => accept.mutate()}
                >
                    {accept.isPending ? t('terms.confirming') : t('terms.continue')}
                </Button>
            </div>
        </div>
    )
}
