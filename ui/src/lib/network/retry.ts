/**
 * Retry policy, kept separate from the axios wiring that applies it.
 *
 * Pure decisions: how long to wait, and whether a given failure may be sent
 * again at all. That second question is a money-safety question here, not a
 * performance one, which is why it lives on its own and is tested on its own.
 */

/**
 * At most two extra attempts. Beyond that a real outage is being hammered
 * rather than ridden out, and the liveness probe is the thing that should be
 * waiting for recovery.
 */
export const MAX_HTTP_RETRIES = 2

/** Statuses that mean "ask again later", as opposed to "here is your answer". */
export const RETRYABLE_HTTP_STATUSES = new Set([408, 425, 429, 500, 502, 503, 504])

export const SAFE_HTTP_METHODS = new Set(['get', 'head', 'options'])

export interface RetryTarget {
  method?: string
  headers?: Record<string, unknown>
  _networkRetryCount?: number
}

/**
 * Full jitter over an exponential ceiling, and a server-supplied Retry-After
 * always wins.
 *
 * The random nudge on the Retry-After path is not decoration: every
 * rate-limited client receives the identical header value, so obeying it to the
 * millisecond returns them all at once and re-triggers the limit.
 */
export function httpRetryDelay(attempt: number, retryAfter?: string, random: () => number = Math.random): number {
  const retryAfterMs = retryAfter == null ? Number.NaN : Number(retryAfter) * 1000
  if (Number.isFinite(retryAfterMs)) return Math.max(0, retryAfterMs) + Math.floor(random() * 250)
  const ceiling = Math.min(3_000, 250 * 2 ** Math.max(0, attempt - 1))
  return Math.floor(random() * ceiling)
}

/**
 * Whether this request may be sent again.
 *
 * Safe methods always may. A mutation may only if it carries an
 * Idempotency-Key, because the server collapses that replay (Invariant #3).
 * Retrying a POST /wallet/deposits without one would open a second PIX charge
 * for one intent — the precise failure the key exists to prevent.
 */
export function retryAllowed(config?: RetryTarget): boolean {
  if (!config || (config._networkRetryCount || 0) >= MAX_HTTP_RETRIES) return false
  const method = (config.method || 'get').toLowerCase()
  if (SAFE_HTTP_METHODS.has(method)) return true
  const headers = config.headers || {}
  return Boolean(headers['Idempotency-Key'] || headers['idempotency-key'])
}

/**
 * A failure worth another attempt. No status at all counts: a timeout, a DNS
 * failure, and a load balancer answering without CORS headers all surface that
 * way, and none of them is evidence that the request was rejected on its
 * merits.
 */
export function isRetryableFailure(error: {
  response?: { status?: number }
  config?: RetryTarget
}): boolean {
  if (!retryAllowed(error.config)) return false
  return error.response?.status === undefined || RETRYABLE_HTTP_STATUSES.has(error.response.status)
}
