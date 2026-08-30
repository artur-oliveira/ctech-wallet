'use client'

import {useEffect, useSyncExternalStore} from 'react'
import {
  type ApiLivenessSnapshot,
  checkApiLiveness,
  getApiLivenessSnapshot,
  getServerApiLivenessSnapshot,
  livenessPollDelay,
  markApiOffline,
  subscribeApiLiveness,
} from '@/lib/network/liveness'

/** Reads the shared snapshot. Does not probe — mount useApiLivenessWatcher once for that. */
export function useApiLiveness(): ApiLivenessSnapshot {
  return useSyncExternalStore(subscribeApiLiveness, getApiLivenessSnapshot, getServerApiLivenessSnapshot)
}

/**
 * The single liveness probe for the app. Mount once, high in the tree.
 *
 * Backs off while the API is down and probes on the events that actually change
 * the answer — the tab becoming visible, the device coming back online — rather
 * than only on a timer. A user who backgrounded a dead tab for an hour should
 * find it recovered when they look at it, not one poll interval later.
 */
export function useApiLivenessWatcher(): ApiLivenessSnapshot {
  const snapshot = useApiLiveness()

  useEffect(() => {
    let cancelled = false
    let timer: ReturnType<typeof setTimeout> | undefined
    let failures = 0

    const schedule = () => {
      if (cancelled) return
      timer = setTimeout(run, livenessPollDelay(failures))
    }

    const run = async () => {
      if (cancelled) return
      // A hidden tab polling a dead API is pure waste; the visibility listener
      // below picks it up the moment the user returns.
      if (typeof document !== 'undefined' && document.visibilityState === 'hidden') return schedule()
      const available = await checkApiLiveness()
      failures = available ? 0 : failures + 1
      schedule()
    }

    void run()

    const onVisible = () => {
      if (document.visibilityState === 'visible') void checkApiLiveness()
    }
    const onOnline = () => void checkApiLiveness()

    document.addEventListener('visibilitychange', onVisible)
    window.addEventListener('online', onOnline)
    window.addEventListener('offline', markApiOffline)

    return () => {
      cancelled = true
      if (timer) clearTimeout(timer)
      document.removeEventListener('visibilitychange', onVisible)
      window.removeEventListener('online', onOnline)
      window.removeEventListener('offline', markApiOffline)
    }
  }, [])

  return snapshot
}
