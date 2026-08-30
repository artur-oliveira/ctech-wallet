'use client'

import {QueryClient, QueryClientProvider} from '@tanstack/react-query'
import {ReactNode, useState} from 'react'
import {ApiError} from '@/lib/api/client'

export function QueryProvider({children}: { children: ReactNode }) {
  const [client] = useState(
    () =>
      new QueryClient({
        defaultOptions: {
          queries: {
            staleTime: 60_000,
            /**
             * The axios layer already retries safe/idempotent requests with
             * jittered backoff and refuses to retry into a known outage
             * (lib/api/client.ts, lib/network/liveness.ts). A second retry
             * budget on top of it multiplies attempts instead of adding
             * resilience, so this one only covers what axios deliberately does
             * not: a request that failed for a reason worth a fresh attempt
             * from the caller's side.
             *
             * 4xx are never retried — a 403 kyc-not-verified or a 409
             * wallet-onboarding is an answer, not a hiccup, and retrying it
             * just delays showing the user their actual next step.
             */
            retry: (failureCount, error) => {
              if (failureCount >= 1) return false
              const status = error instanceof ApiError ? error.status : 0
              return status === 0 || status >= 500
            },
          },
        },
      }),
  )

  return <QueryClientProvider client={client}>{children}</QueryClientProvider>
}
