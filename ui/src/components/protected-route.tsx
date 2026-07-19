'use client'

import React, {useEffect} from 'react'
import {useRouter} from 'next/navigation'
import {useQuery} from '@tanstack/react-query'
import {apiClient} from '@/lib/api/client'
import {useAuth} from '@/lib/hooks/useAuth'
import {TermsAddendumGate} from '@/components/terms-addendum-gate'
import {useTranslation} from 'react-i18next'

function Spinner() {
    const {t} = useTranslation()
    return (
        <div className="flex min-h-screen items-center justify-center" role="status">
            <div aria-hidden="true" className="size-10 animate-spin rounded-full border-4 border-brand-200 border-t-brand-600"/>
            <span className="sr-only">{t('common.loading')}</span>
        </div>
    )
}

export function ProtectedRoute({children}: { children: React.ReactNode }) {
    const {authenticated, loading} = useAuth()
    const router = useRouter()

    const me = useQuery({
        queryKey: ['me'],
        queryFn: () => apiClient.me(),
        enabled: authenticated,
    })

    useEffect(() => {
        if (!loading && !authenticated) router.replace('/login')
    }, [loading, authenticated, router])

    if (loading || !authenticated || me.isLoading) {
        return <Spinner/>
    }

    // The terms gate replaces the app entirely until the current addendum is accepted.
    if (me.data && !me.data.terms_addendum_accepted) {
        return <TermsAddendumGate/>
    }

    return <>{children}</>
}
