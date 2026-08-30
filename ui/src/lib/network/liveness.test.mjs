import assert from 'node:assert/strict'
import test from 'node:test'

import {
  HEALTHY_POLL_INTERVAL_MS,
  HTTP_TIMEOUT_MS,
  livenessPollDelay,
  MAX_UNAVAILABLE_POLL_INTERVAL_MS,
} from './liveness.ts'
import {httpRetryDelay, isRetryableFailure} from './retry.ts'

test('a healthy API is polled on a steady cadence, never backed off', () => {
  assert.equal(livenessPollDelay(0), HEALTHY_POLL_INTERVAL_MS)
  assert.equal(livenessPollDelay(-1), HEALTHY_POLL_INTERVAL_MS)
})

test('an unavailable API backs off exponentially and stops at the ceiling', () => {
  const worst = () => 1
  assert.ok(livenessPollDelay(1, worst) <= 1_000)
  assert.ok(livenessPollDelay(2, worst) <= 2_000)
  assert.ok(livenessPollDelay(3, worst) <= 4_000)
  // Never grows without bound: a tab open overnight must not schedule an
  // hour-long gap and miss the recovery entirely.
  assert.ok(livenessPollDelay(50, worst) <= MAX_UNAVAILABLE_POLL_INTERVAL_MS)
})

test('equal jitter never returns a near-zero delay', () => {
  // Full jitter would allow ~0 here and busy-loop a dead API. Half the ceiling
  // is fixed precisely so the floor stays meaningful.
  for (const failures of [1, 2, 5, 9]) {
    const floor = livenessPollDelay(failures, () => 0)
    assert.ok(floor > 0, `failures=${failures} produced a ${floor}ms delay`)
  }
})

test('jitter spreads clients instead of synchronizing them', () => {
  const low = livenessPollDelay(4, () => 0)
  const high = livenessPollDelay(4, () => 0.999)
  assert.ok(high > low, 'identical delays across clients thundering-herd the recovery')
})

test('the HTTP timeout is long enough for a slow round trip and short enough to be honest', () => {
  assert.ok(HTTP_TIMEOUT_MS >= 5_000 && HTTP_TIMEOUT_MS <= 15_000)
})

test('retry backoff honours Retry-After, plus a jitter nudge', () => {
  // Every rate-limited client receives the same header value, so obeying it
  // exactly returns them all in the same millisecond.
  const delay = httpRetryDelay(1, '2', () => 0.5)
  assert.ok(delay >= 2_000 && delay < 2_250)
})

test('retry backoff is bounded when no Retry-After is given', () => {
  for (const attempt of [1, 2, 3, 10]) {
    assert.ok(httpRetryDelay(attempt, undefined, () => 0.999) <= 3_000)
  }
})

test('only safe or idempotent requests are retried', () => {
  const retryable = (config) => isRetryableFailure({response: {status: 500}, config})

  assert.equal(retryable({method: 'get'}), true)
  // A POST without an idempotency key must never be replayed: two PIX charges
  // for one intent is exactly what the key exists to prevent.
  assert.equal(retryable({method: 'post'}), false)
  assert.equal(retryable({method: 'post', headers: {'Idempotency-Key': 'k1'}}), true)
  assert.equal(retryable({method: 'post', headers: {'idempotency-key': 'k1'}}), true)
})

test('the retry budget is finite', () => {
  assert.equal(isRetryableFailure({response: {status: 500}, config: {method: 'get', _networkRetryCount: 2}}), false)
})

test('a client error is an answer, not a hiccup', () => {
  const failed = (status) => isRetryableFailure({response: {status}, config: {method: 'get'}})
  // 409 wallet-onboarding and 403 kyc-not-verified are the server telling the
  // user their next step. Retrying them only delays showing it.
  for (const status of [400, 401, 403, 404, 409, 422]) {
    assert.equal(failed(status), false, `${status} must not be retried`)
  }
  for (const status of [408, 429, 500, 502, 503, 504]) {
    assert.equal(failed(status), true, `${status} should be retried`)
  }
})

test('a failure with no response at all is retryable', () => {
  // A timeout or a CORS-shaped outage exposes no status; treating it as
  // non-retryable would give up on an ordinary network blip.
  assert.equal(isRetryableFailure({config: {method: 'get'}}), true)
})
