'use client'

import React, {useEffect} from 'react'
import {useRouter} from 'next/navigation'
import {useQuery} from '@tanstack/react-query'
import {ShieldAlert} from 'lucide-react'
import {apiClient} from '@/lib/api/client'
import {useAuth} from '@/lib/hooks/useAuth'
import {TermsAddendumGate} from '@/components/terms-addendum-gate'
import {useTranslation} from 'react-i18next'
import {Button} from '@/components/ui/button'
import {LanguageSwitcher} from '@/components/language-switcher'
import {QUERY_KEY_ME} from '@/lib/constants/query'
import {resolveProtectedRouteState} from '@/lib/utils/protected-route-state'

function Spinner() {
    const {t} = useTranslation()
    return (
        <div className="flex min-h-screen items-center justify-center" role="status">
            <div aria-hidden="true" className="size-10 animate-spin rounded-full border-4 border-brand-200 border-t-brand-600"/>
            <span className="sr-only">{t('common.loading')}</span>
        </div>
    )
}

function ConsentLookupError({
                                retrying,
                                onRetry,
                                onLogout,
                            }: {
    retrying: boolean
    onRetry: () => void
    onLogout: () => void
}) {
    const {t} = useTranslation()

    return (
        <div className="flex min-h-screen items-center justify-center bg-background px-4 py-4">
            <main className="w-full max-w-md space-y-5 rounded-2xl border border-border bg-card p-6">
                <div className="flex items-center justify-between gap-4">
                    <div className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-destructive/10 text-destructive">
                        <ShieldAlert aria-hidden="true" size={20}/>
                    </div>
                    <LanguageSwitcher/>
                </div>

                <div role="alert">
                    <h1 className="text-lg font-semibold text-foreground">{t('terms.lookupErrorTitle')}</h1>
                    <p className="mt-1 text-sm leading-relaxed text-muted-foreground">
                        {t('terms.lookupErrorDescription')}
                    </p>
                </div>

                <div className="flex flex-col-reverse gap-2 sm:flex-row">
                    <Button variant="outline" className="flex-1 whitespace-normal" onClick={onLogout}>
                        {t('terms.lookupSignOut')}
                    </Button>
                    <Button
                        variant="brand"
                        className="flex-1 whitespace-normal"
                        disabled={retrying}
                        onClick={onRetry}
                    >
                        {retrying ? t('terms.lookupRetrying') : t('common.tryAgain')}
                    </Button>
                </div>
            </main>
        </div>
    )
}

export function ProtectedRoute({children}: { children: React.ReactNode }) {
    const {authenticated, loading, logout} = useAuth()
    const router = useRouter()

    const me = useQuery({
        queryKey: QUERY_KEY_ME,
        queryFn: () => apiClient.me(),
        enabled: authenticated,
    })

    useEffect(() => {
        if (!loading && !authenticated) router.replace('/login')
    }, [loading, authenticated, router])

    const state = resolveProtectedRouteState({
        authLoading: loading,
        authenticated,
        profileLoading: me.isPending,
        profile: me.data,
    })

    if (state === 'loading') {
        return <Spinner/>
    }

    if (state === 'lookup-error') {
        return (
            <ConsentLookupError
                retrying={me.isFetching}
                onRetry={() => void me.refetch()}
                onLogout={logout}
            />
        )
    }

    // The terms gate replaces the app entirely until the current addendum is accepted.
    if (state === 'terms-required') {
        return <TermsAddendumGate/>
    }

    return <>{children}</>
}
