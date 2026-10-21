import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import test from 'node:test'

import {
  ApiUnavailableError,
  assertApiReachable,
  classifyApiFailure,
  setApiReachability,
} from './api-reachability.ts'

const read = (path) => readFile(new URL(path, import.meta.url), 'utf8')
const clientSource = await read('../api/client.ts')
const boundarySource = await read('../../components/network-boundary.tsx')
const unavailableSource = await read('../../app/unavailable/unavailable-state.tsx')

test('ordinary calls fail fast while the device is offline or the API is confirmed down', () => {
  setApiReachability('online')
  assert.doesNotThrow(assertApiReachable)
  // degraded is a blip, not an outage: calls still go through.
  setApiReachability('degraded')
  assert.doesNotThrow(assertApiReachable)
  setApiReachability('offline')
  assert.throws(assertApiReachable, (e) => e instanceof ApiUnavailableError && e.reason === 'offline')
  setApiReachability('unavailable')
  assert.throws(assertApiReachable, (e) => e instanceof ApiUnavailableError && e.reason === 'server')
  setApiReachability('online')
})

test('the fail-fast error is never retried into an outage', () => {
  assert.equal(classifyApiFailure(new ApiUnavailableError('server')).aborted, true)
})

test('a failure with no response (status 0) is retryable, a status keeps its Retry-After', () => {
  // withRetry treats an undefined status as a timeout/DNS/CORS-shaped outage.
  assert.equal(classifyApiFailure({status: 0}).status, undefined)
  assert.deepEqual(classifyApiFailure({status: 429, retryAfter: '2'}), {status: 429, retryAfter: '2'})
})

test('the API client retries only through withRetry, and a mutation only with an Idempotency-Key', () => {
  assert.match(clientSource, /withRetry\(/)
  // Money safety: replay is allowed for a mutation only when it carries the key.
  assert.match(clientSource, /idempotent: Boolean\(config\.headers\?\.\['Idempotency-Key'\]\)/)
  assert.match(clientSource, /classify: classifyApiFailure/)
  // No second, local retry loop next to it, and no local liveness/retry modules.
  assert.doesNotMatch(clientSource, /_networkRetryCount|httpRetryDelay|isRetryableFailure|requireApiLiveness/)
  assert.doesNotMatch(clientSource, /maxRetries/)
})

test('deposits and withdrawals always send their Idempotency-Key', () => {
  assert.match(clientSource, /createDeposit[\s\S]*?idemConfig\(idempotencyKey\)/)
  assert.match(clientSource, /createWithdrawal[\s\S]*?idemConfig\(idempotencyKey\)/)
})

test('a 503 redirects to /unavailable only after retries are exhausted', () => {
  // The redirect is in the catch around withRetry, not in the per-attempt interceptor.
  assert.match(clientSource, /catch \(error\) \{[\s\S]*redirectOnServiceUnavailable/)
  const interceptor = clientSource.slice(clientSource.indexOf('interceptors.response.use'), clientSource.indexOf('/** Options for mutating'))
  assert.doesNotMatch(interceptor, /redirectOnServiceUnavailable/)
})

test('the network boundary owns the one probe, refetches active queries on reconnect and routes to /unavailable', () => {
  assert.match(boundarySource, /<NetworkProvider/)
  assert.match(boundarySource, /\/v1\.0\/health/)
  assert.match(boundarySource, /onReconnect=\{\(\) => void queryClient\.refetchQueries\(\{type: 'active'\}\)\}/)
  assert.match(boundarySource, /onUnavailable=\{\(\) => redirectOnServiceUnavailable\(503\)\}/)
  assert.match(boundarySource, /setApiReachability\(status\)/)
  // Product copy, not the package default.
  assert.match(boundarySource, /systemState\.offline\.title/)
})

test('/unavailable uses UnavailableScreen and returns the user to the remembered route', () => {
  assert.match(unavailableSource, /<UnavailableScreen/)
  assert.match(unavailableSource, /onRecovered=\{goBack\}/)
  assert.match(unavailableSource, /SESSION_KEY_RETURN_AFTER_OUTAGE/)
})

test('error, not-found and unavailable pages render the shared states', async () => {
  assert.match(await read('../../app/error.tsx'), /<ErrorState[\s\S]*status=\{500\}/)
  assert.match(await read('../../app/not-found.tsx'), /<ErrorState[\s\S]*status=\{404\}/)
})
