import assert from 'node:assert/strict'
import test from 'node:test'

import {MAX_AUTH_RECOVERIES, realtimeAuthAction} from './realtime-auth.ts'

const fresh = {recoveries: 0}

test('ordinary frames do not touch the auth lifecycle', () => {
  for (const message of [
    {type: 'deposit_confirmed', txid: 'tx1'},
    {type: 'ping'},
    {type: 'connected'},
    {type: 'error', code: 'wallet_busy'},
    null,
    undefined,
    {},
  ]) {
    assert.equal(realtimeAuthAction(message, fresh), 'ignore')
  }
})

test('a first unauthorized frame is worth one token renewal', () => {
  assert.equal(realtimeAuthAction({type: 'error', code: 'unauthorized'}, fresh), 'refresh')
})

test('a second unauthorized frame stops the socket instead of reconnecting', () => {
  // This is the whole bug: the server accepts the upgrade and rejects the auth
  // frame, so the client library sees a successful open and resets its backoff.
  // Without a terminal verdict, an expired token reconnects forever.
  assert.equal(
    realtimeAuthAction({type: 'error', code: 'unauthorized'}, {recoveries: MAX_AUTH_RECOVERIES}),
    'stop',
  )
})

test('the recovery budget never reopens with more attempts', () => {
  for (const recoveries of [1, 2, 50]) {
    assert.equal(realtimeAuthAction({type: 'error', code: 'unauthorized'}, {recoveries}), 'stop')
  }
})
