'use client'

import {Suspense, useEffect, useRef, useState} from 'react'
import {useRouter, useSearchParams} from 'next/navigation'
import {useTranslation} from 'react-i18next'
import {useAuth} from '@/lib/hooks/useAuth'
import {exchangeCode} from '@/lib/auth/oauth'
import {callbackErrorKey} from '@/lib/auth/callback-error'
import {Button} from '@/components/ui/button'
import {LanguageSwitcher} from '@/components/language-switcher'

function CallbackInner() {
  const {t} = useTranslation()
  const {handleCallback} = useAuth()
  const router = useRouter()
  const searchParams = useSearchParams()
  const [asyncErrorKey, setAsyncErrorKey] = useState<string | null>(null)
  const ran = useRef(false)

  const code = searchParams.get('code')
  const state = searchParams.get('state')
  const errorParam = searchParams.get('error')

  const paramErrorKey = errorParam
    ? callbackErrorKey(errorParam)
    : !code || !state
      ? 'invalidCallback'
      : null

  const errorKey = paramErrorKey ?? asyncErrorKey

  useEffect(() => {
    if (ran.current || paramErrorKey) return
    ran.current = true

    void (async () => {
      try {
        const {accessToken, idToken, returnTo} = await exchangeCode(code!, state!)
        await handleCallback(accessToken, idToken)
        router.replace(returnTo)
      } catch {
        setAsyncErrorKey('generic')
      }
    })()
  }, [handleCallback, router, paramErrorKey, code, state])

  if (errorKey) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-background px-4 py-6">
        <main className="w-full max-w-md space-y-5 rounded-xl border border-border bg-card p-6">
          <div className="flex justify-end">
            <LanguageSwitcher/>
          </div>
          <div role="alert">
            <h1 className="text-lg font-semibold text-foreground">
              {t(`callback.error.${errorKey}.title`)}
            </h1>
            <p className="mt-1 text-sm leading-relaxed text-muted-foreground">
              {t(`callback.error.${errorKey}.description`)}
            </p>
          </div>
          <Button variant="brand" className="w-full whitespace-normal" onClick={() => router.replace('/login')}>
            {t('callback.returnToLogin')}
          </Button>
        </main>
      </div>
    )
  }

  return (
    <div className="min-h-screen flex items-center justify-center">
      <div className="text-center space-y-2" role="status">
        <div
          aria-hidden="true"
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
