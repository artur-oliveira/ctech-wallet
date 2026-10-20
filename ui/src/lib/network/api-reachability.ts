/**
 * Bridge between the NetworkProvider (@aoctech/ui owns the single health probe)
 * and non-React code: the API client reads the last status from here so ordinary
 * calls fail fast during an outage instead of each one becoming its own retry loop.
 */
import type {RetryFailure} from '@aoctech/ui'

export type ApiReachability = 'online' | 'degraded' | 'offline' | 'unavailable'

let current: ApiReachability = 'online'

export function setApiReachability(status: ApiReachability): void {
  current = status
}

export class ApiUnavailableError extends Error {
  // Plain field, not a parameter property: node:test type-strips this file.
  readonly reason: 'offline' | 'server'

  constructor(reason: 'offline' | 'server') {
    super(reason === 'offline' ? 'Device is offline' : 'Wallet API is unavailable')
    this.name = 'ApiUnavailableError'
    this.reason = reason
  }
}

/** Throws while the probe says the device is offline or the API is confirmed down. */
export function assertApiReachable(): void {
  if (current === 'offline') throw new ApiUnavailableError('offline')
  if (current === 'unavailable') throw new ApiUnavailableError('server')
}

/**
 * Maps what the API client throws to withRetry's view of it. An ApiError with
 * status 0 is a timeout/DNS/no-response failure (retryable, like no status at
 * all); the fail-fast error is never retried into a known outage.
 */
export function classifyApiFailure(error: unknown): RetryFailure {
  if (error instanceof ApiUnavailableError) return {aborted: true}
  const e = error as {status?: number; retryAfter?: string}
  return {status: e?.status || undefined, retryAfter: e?.retryAfter}
}
