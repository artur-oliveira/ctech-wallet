'use client'

import {useEffect, useMemo, type ReactNode} from 'react'
import {useQueryClient} from '@tanstack/react-query'
import {NetworkProvider, useNetwork} from '@aoctech/ui'
import {useTranslation} from 'react-i18next'
import {redirectOnServiceUnavailable} from '@/lib/api/client'
import {USE_MOCK} from '@/lib/mock'
import {normalizeLocale} from '@/lib/locale'
import {setApiReachability} from '@/lib/network/api-reachability'

const HEALTH_URL = `${(process.env.NEXT_PUBLIC_API_URL || '').replace(/\/$/, '')}/v1.0/health`
// Mock mode has no backend to probe.
const mockFetch = (() => Promise.resolve(new Response(null, {status: 200}))) as typeof fetch

/** Publishes the probe's status for the API client's fail-fast check. */
function ReachabilityBridge() {
  const {status} = useNetwork()
  useEffect(() => setApiReachability(status), [status])
  return null
}

/**
 * The app's one health probe: an offline/degraded banner, a redirect to
 * /unavailable after consecutive server failures, and a refetch of every active
 * query when the connection comes back. Copy is the product's own (systemState.*).
 */
export function NetworkBoundary({children}: { children: ReactNode }) {
  const {t, i18n} = useTranslation()
  const queryClient = useQueryClient()
  const locale = normalizeLocale(i18n.language)
  const labels = useMemo(
    () => ({
      offline: t('systemState.offline.title'),
      offlineHint: t('systemState.offline.description'),
      unavailable: t('systemState.unavailable.title'),
      unavailableHint: t('systemState.unavailable.description'),
      retry: t('systemState.checkAgain'),
      checking: t('systemState.unavailable.checking'),
    }),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [t, i18n.language],
  )

  return (
    <NetworkProvider
      url={HEALTH_URL}
      fetch={USE_MOCK ? mockFetch : undefined}
      locale={locale}
      labels={labels}
      onReconnect={() => void queryClient.refetchQueries({type: 'active'})}
      onUnavailable={() => redirectOnServiceUnavailable(503)}
    >
      <ReachabilityBridge/>
      {children}
    </NetworkProvider>
  )
}
