'use client'

import {useEffect} from 'react'
import {useTranslation} from 'react-i18next'
import {SystemState, SystemStateSupport} from '@/components/system-state'
import {ACCOUNT_SUPPORT_URL} from '@/lib/legal'
import {useAuth} from '@/lib/hooks/useAuth'

/**
 * Route-level boundary: one screen crashed, the app did not.
 *
 * `reset` re-renders the segment without a full reload, which is the right first
 * move here — the balance the user came to see is still in the query cache.
 * The digest is surfaced because it is the only handle support has on a
 * minified stack.
 */
export default function ErrorPage({error, reset}: { error: Error & { digest?: string }; reset: () => void }) {
  const {t} = useTranslation()
  const {authenticated} = useAuth()

  useEffect(() => {
    console.error(error)
  }, [error])

  return (
    <SystemState
      code="500"
      readout={t('systemState.error.readout')}
      title={t('systemState.error.title')}
      description={t('systemState.error.description')}
      detail={error.digest ? t('systemState.error.reference', {digest: error.digest}) : t('systemState.error.detail')}
      onRetry={reset}
      retryLabel={t('systemState.retry')}
      homeHref={authenticated ? '/dashboard' : '/login'}
      homeLabel={authenticated ? t('systemState.backToDashboard') : t('systemState.backToLogin')}
      reassurance={t('systemState.reassurance')}
    >
      <SystemStateSupport href={ACCOUNT_SUPPORT_URL} label={t('systemState.support')}/>
    </SystemState>
  )
}
