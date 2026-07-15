'use client'

import {Suspense, useEffect, useRef, useState} from 'react'
import {useRouter, useSearchParams} from 'next/navigation'
import {useTranslation} from 'react-i18next'
import {useAuth} from '@/lib/hooks/useAuth'
import {exchangeCode} from '@/lib/auth/oauth'

function CallbackInner() {
    const {t} = useTranslation()
    const {handleCallback} = useAuth()
    const router = useRouter()
    const searchParams = useSearchParams()
    const [asyncError, setAsyncError] = useState<string | null>(null)
    const ran = useRef(false)

    const code = searchParams.get('code')
    const state = searchParams.get('state')
    const errorParam = searchParams.get('error')

    const paramError = errorParam
        ? `${t('callback.errorPrefix')}${errorParam}`
        : !code || !state
            ? t('callback.invalidParams')
            : null

    const error = paramError ?? asyncError

    useEffect(() => {
        if (ran.current || paramError) return
        ran.current = true

        void (async () => {
            try {
                const {accessToken, idToken, returnTo} = await exchangeCode(code!, state!)
                await handleCallback(accessToken, idToken)
                router.replace(returnTo)
            } catch (err) {
                setAsyncError(err instanceof Error ? err.message : t('common.genericError'))
            }
        })()
    }, [searchParams, handleCallback, router, paramError, code, state, t])

    if (error) {
        return (
            <div className="min-h-screen flex items-center justify-center">
                <div className="text-center space-y-4 max-w-sm">
                    <p className="text-red-600 text-sm">{error}</p>
                    <button
                        className="text-primary-600 underline text-sm"
                        onClick={() => router.push('/login')}
                    >
                        {t('callback.tryAgain')}
                    </button>
                </div>
            </div>
        )
    }

    return (
        <div className="min-h-screen flex items-center justify-center">
            <div className="text-center space-y-2">
                <div
                    className="w-10 h-10 border-4 border-primary-200 border-t-primary-600 rounded-full animate-spin mx-auto"/>
                <p className="text-muted-foreground text-sm">{t('callback.authenticating')}</p>
            </div>
        </div>
    )
}

export default function CallbackPage() {
    return (
        <Suspense>
            <CallbackInner/>
        </Suspense>
    )
}
