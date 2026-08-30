/**
 * API reachability, tracked in one place.
 *
 * Without this, every mounted query becomes its own availability probe: a dead
 * API turns N screens into N retry loops that all fail the same way and all
 * report it separately. One health probe owns the question "is the API up", and
 * ordinary calls fail fast against its answer.
 *
 * `/v1.0/health` is the dependency-free liveness route (it does not touch
 * DynamoDB, cache, or PIX), so it stays answerable exactly when everything
 * behind it is not.
 */

/**
 * 10s covers a cold Lambda behind pix-gateway and a slow mobile network, and
 * still fails inside a user's patience. Anything longer and the UI is lying
 * about being busy; anything shorter and a legitimate PIX charge round-trip
 * gets cancelled mid-flight.
 */
export const HTTP_TIMEOUT_MS = 10_000
/** Liveness alone gets a tighter budget — it is one dependency-free GET. */
export const LIVENESS_TIMEOUT_MS = 5_000
export const HEALTHY_POLL_INTERVAL_MS = 30_000
export const MAX_UNAVAILABLE_POLL_INTERVAL_MS = 30_000

export type ApiLivenessStatus = 'checking' | 'available' | 'unavailable'
/** `offline` is the device's own network; `server` is everything else. */
export type ApiUnavailableReason = 'offline' | 'server' | null

export interface ApiLivenessSnapshot {
  status: ApiLivenessStatus
  reason: ApiUnavailableReason
  checkedAt: number | null
}

export class ApiUnavailableError extends Error {
  // A plain field rather than a constructor parameter property: this module is
  // imported directly by the node:test suite, which type-strips rather than
  // compiles, and parameter properties are not erasable syntax.
  readonly reason: Exclude<ApiUnavailableReason, null>

  constructor(reason: Exclude<ApiUnavailableReason, null>) {
    super(reason === 'offline' ? 'Device is offline' : 'Wallet API is unavailable')
    this.name = 'ApiUnavailableError'
    this.reason = reason
  }
}

const INITIAL_SNAPSHOT: ApiLivenessSnapshot = {status: 'checking', reason: null, checkedAt: null}

let snapshot: ApiLivenessSnapshot = INITIAL_SNAPSHOT
let inFlightCheck: Promise<boolean> | null = null
const listeners = new Set<() => void>()

function apiURL(path: string): string {
  const base = (process.env.NEXT_PUBLIC_API_URL || '').replace(/\/$/, '')
  return `${base}${path}`
}

function publish(next: ApiLivenessSnapshot): void {
  if (snapshot.status === next.status && snapshot.reason === next.reason && snapshot.checkedAt === next.checkedAt) return
  snapshot = next
  listeners.forEach((listener) => listener())
}

function browserIsOffline(): boolean {
  return typeof navigator !== 'undefined' && !navigator.onLine
}

export function getApiLivenessSnapshot(): ApiLivenessSnapshot {
  return snapshot
}

/** Server render has never probed anything, so it must report `checking`. */
export function getServerApiLivenessSnapshot(): ApiLivenessSnapshot {
  return INITIAL_SNAPSHOT
}

export function subscribeApiLiveness(listener: () => void): () => void {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

export function markApiOffline(): void {
  publish({status: 'unavailable', reason: 'offline', checkedAt: Date.now()})
}

/**
 * The only request allowed while the API is unavailable.
 *
 * A fetch rejection is treated exactly like a failed response, deliberately: a
 * dead load balancer answers without CORS headers, and the browser surfaces
 * that as a TypeError rather than a status — reading only `response.status`
 * would classify a total outage as "unknown" and keep retrying into it.
 *
 * Concurrent callers share one in-flight probe, so a screen with eight queries
 * does not open eight health checks.
 */
export function checkApiLiveness(): Promise<boolean> {
  if (browserIsOffline()) {
    markApiOffline()
    return Promise.resolve(false)
  }
  if (inFlightCheck) return inFlightCheck

  const controller = new AbortController()
  const timeout = setTimeout(() => controller.abort(), LIVENESS_TIMEOUT_MS)
  inFlightCheck = fetch(apiURL('/v1.0/health'), {
    method: 'GET',
    cache: 'no-store',
    credentials: 'omit',
    headers: {Accept: 'application/json'},
    signal: controller.signal,
  })
    .then((response) => {
      const available = response.ok
      publish({
        status: available ? 'available' : 'unavailable',
        reason: available ? null : 'server',
        checkedAt: Date.now(),
      })
      return available
    })
    .catch(() => {
      publish({
        status: 'unavailable',
        reason: browserIsOffline() ? 'offline' : 'server',
        checkedAt: Date.now(),
      })
      return false
    })
    .finally(() => {
      clearTimeout(timeout)
      inFlightCheck = null
    })
  return inFlightCheck
}

/**
 * Ordinary calls wait for the first health answer, then fail fast while the API
 * is down. This is what makes the health probe — and not every screen's query —
 * the loop responsible for noticing recovery.
 */
export async function requireApiLiveness(): Promise<void> {
  if (snapshot.status === 'available') return
  if (snapshot.status === 'unavailable') {
    throw new ApiUnavailableError(snapshot.reason || 'server')
  }
  if (!(await checkApiLiveness())) throw new ApiUnavailableError(snapshot.reason || 'server')
}

/**
 * Poll cadence for the liveness watcher: steady while healthy, exponential with
 * equal jitter while down.
 *
 * Equal jitter (half the ceiling fixed, half random) rather than full jitter:
 * full jitter can return a near-zero delay and busy-loop a dead API, while no
 * jitter marches every open tab in lockstep and thundering-herds the service at
 * the exact moment it comes back.
 */
export function livenessPollDelay(failureCount: number, random: () => number = Math.random): number {
  if (failureCount <= 0) return HEALTHY_POLL_INTERVAL_MS
  const ceiling = Math.min(MAX_UNAVAILABLE_POLL_INTERVAL_MS, 1_000 * 2 ** (failureCount - 1))
  return Math.floor(ceiling / 2 + (random() * ceiling) / 2)
}

/** Test-only reset, kept explicit so runtime code cannot accidentally hide an outage. */
export function resetApiLivenessForTests(): void {
  snapshot = INITIAL_SNAPSHOT
  inFlightCheck = null
  listeners.clear()
}
