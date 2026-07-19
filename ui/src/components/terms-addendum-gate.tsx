'use client'

import {useState} from 'react'
import {useMutation, useQueryClient} from '@tanstack/react-query'
import {ShieldCheck} from 'lucide-react'
import {useTranslation} from 'react-i18next'
import {apiClient} from '@/lib/api/client'
import {Button} from '@/components/ui/button'
import {WALLET_TERMS_URL} from '@/lib/legal'
import {useAuth} from '@/lib/hooks/useAuth'
import {LanguageSwitcher} from '@/components/language-switcher'
import {QUERY_KEY_ME} from '@/lib/constants/query'

/**
 * Blocks the whole app until the user accepts the wallet's terms addendum.
 *
 * The wallet custodies real money, which is not covered by the account's master
 * terms, and an SSO sign-up never presents a checkbox for product-specific terms.
 * Game and sandbox wallets remain a separate, optional consent. Acceptance is
 * checked against the current version, so bumping it re-gates everyone.
 */
export function TermsAddendumGate() {
    const {t} = useTranslation()
    const {logout} = useAuth()
    const qc = useQueryClient()
    const [checked, setChecked] = useState(false)

    const accept = useMutation({
        mutationFn: () => apiClient.acceptTermsAddendum(),
        onSuccess: () => qc.invalidateQueries({queryKey: QUERY_KEY_ME}),
    })

    return (
        <div className="flex min-h-screen items-center justify-center bg-background px-4 py-4">
            <div className="w-full max-w-md space-y-5 rounded-2xl border border-brand-100 bg-card p-6">
                <div className="flex items-center justify-between">
                    <div className="flex size-10 items-center justify-center rounded-lg bg-brand-600 text-white">
                        <ShieldCheck aria-hidden="true" size={20}/>
                    </div>
                    <LanguageSwitcher/>
                </div>

                <div>
                    <h1 className="text-lg font-semibold text-foreground">{t('terms.title')}</h1>
                    <p className="mt-1 text-sm leading-relaxed text-muted-foreground">
                        {t('terms.description')}
                    </p>
                </div>

                {accept.isError && (
                    <p role="alert" className="text-sm text-destructive">{t('terms.error')}</p>
                )}

                <label className="flex min-h-11 cursor-pointer items-start gap-2 text-sm text-muted-foreground">
                    <input
                        type="checkbox"
                        checked={checked}
                        onChange={(e) => setChecked(e.target.checked)}
                        className="mt-0.5 size-4 shrink-0 rounded border-border accent-brand-600 focus-visible:outline-none focus-visible:ring-3 focus-visible:ring-brand-500/30"
                    />
                    <span>
            {t('terms.checkboxPrefix')}{' '}
                        <a
                            href={WALLET_TERMS_URL}
                            target="_blank"
                            rel="noreferrer"
                            className="text-foreground underline underline-offset-4"
                        >
              {t('terms.termsLink')}
            </a>
            .
          </span>
                </label>

                <div className="flex flex-col-reverse gap-2 sm:flex-row">
                    <Button variant="outline" className="flex-1" onClick={logout}>
                        {t('terms.decline')}
                    </Button>
                    <Button
                        variant="brand"
                        className="flex-1"
                        disabled={!checked || accept.isPending}
                        onClick={() => accept.mutate()}
                    >
                        {accept.isPending ? t('terms.confirming') : t('terms.continue')}
                    </Button>
                </div>
            </div>
        </div>
    )
}
