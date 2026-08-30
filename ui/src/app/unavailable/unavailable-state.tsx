'use client'

import {useTranslation} from 'react-i18next'
import {SystemState} from '@/components/system-state'
import {useApiLivenessWatcher} from '@/lib/hooks/useApiLiveness'
import {checkApiLiveness} from '@/lib/network/liveness'
import {SESSION_KEY_RETURN_AFTER_OUTAGE} from '@/lib/constants/storage'

const FALLBACK_DESTINATION = '/dashboard'

/**
 * The maintenance screen, reached when the API answers 503 or stops answering.
 *
 * It is not a dead end: the liveness watcher keeps probing with backoff, so the
 * page returns the user on its own the moment the API is back, and the retry
 * button is a way to ask now rather than the only way to leave. Reloading blind
 * is what we are replacing — a user hammering F5 against a dead API learns
 * nothing and adds load.
 */
export function UnavailableState() {
  const {t} = useTranslation()
  const liveness = useApiLivenessWatcher()
  const checking = liveness.status === 'checking'
  const offline = liveness.reason === 'offline'

  const goBack = () => {
    let destination = FALLBACK_DESTINATION
    try {
      destination = window.sessionStorage.getItem(SESSION_KEY_RETURN_AFTER_OUTAGE) || destination
      window.sessionStorage.removeItem(SESSION_KEY_RETURN_AFTER_OUTAGE)
    } catch {
      // Storage unavailable — the dashboard is the safe destination.
    }
    window.location.replace(destination)
  }

  // Recovery is automatic: the watcher flipped to available, so leaving is the
  // correct next move without asking the user to press anything.
  if (liveness.status === 'available') goBack()

  const retry = async () => {
    if (checking) return
    if (await checkApiLiveness()) goBack()
  }

  return (
    <SystemState
      code="503"
      readout={t(offline ? 'systemState.offline.readout' : 'systemState.unavailable.readout')}
      title={t(offline ? 'systemState.offline.title' : 'systemState.unavailable.title')}
      description={t(offline ? 'systemState.offline.description' : 'systemState.unavailable.description')}
      detail={t(checking ? 'systemState.unavailable.checking' : 'systemState.unavailable.detail')}
      onRetry={() => void retry()}
      retryLabel={t('systemState.checkAgain')}
      retryPending={checking}
      homeHref={FALLBACK_DESTINATION}
      homeLabel={t('systemState.backToDashboard')}
      reassurance={t('systemState.reassurance')}
    />
  )
}
