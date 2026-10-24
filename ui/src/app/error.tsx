'use client'

import {useEffect} from 'react'
import Link from 'next/link'
import {ErrorState} from '@aoctech/ui'
import {useTranslation} from 'react-i18next'
import {Button} from '@aoctech/ui'
import {ACCOUNT_SUPPORT_URL} from '@/lib/legal'
import {normalizeLocale} from '@/lib/locale'
import {useAuth} from '@/lib/hooks/useAuth'

/**
 * Route-level boundary: one screen crashed, the app did not.
 *
 * `reset` re-renders the segment without a full reload, which is the right first
 * move here: the balance the user came to see is still in the query cache.
 * The digest is surfaced because it is the only handle support has on a
 * minified stack.
 */
export default function ErrorPage({error, reset}: { error: Error & { digest?: string }; reset: () => void }) {
  const {t, i18n} = useTranslation()
  const {authenticated} = useAuth()

  useEffect(() => {
    console.error(error)
  }, [error])

  return (
    <ErrorState
      status={500}
      locale={normalizeLocale(i18n.language)}
      title={t('systemState.error.title')}
      description={[
        t('systemState.error.description'),
        error.digest ? t('systemState.error.reference', {digest: error.digest}) : t('systemState.error.detail'),
        t('systemState.reassurance'),
      ].join(' ')}
      action={
        <>
          <Button variant="brand"  onClick={reset}>{t('systemState.retry')}</Button>
          <Button variant="outline"  render={<Link href={authenticated ? '/dashboard' : '/login'}/>}>
            {authenticated ? t('systemState.backToDashboard') : t('systemState.backToLogin')}
          </Button>
          <Button variant="ghost"  render={<a href={ACCOUNT_SUPPORT_URL}/>}>{t('systemState.support')}</Button>
        </>
      }
    />
  )
}
