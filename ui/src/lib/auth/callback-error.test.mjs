import assert from 'node:assert/strict'
import test from 'node:test'

import {callbackErrorKey} from './callback-error.ts'

test('OAuth callback errors map to safe user-facing categories', () => {
    assert.equal(callbackErrorKey('access_denied'), 'accessDenied')
    assert.equal(callbackErrorKey('login_required'), 'sessionExpired')
    assert.equal(callbackErrorKey('server_error'), 'unavailable')
    assert.equal(callbackErrorKey('invalid_request'), 'invalidRequest')
})

test('unknown OAuth provider text is never returned for display', () => {
    assert.equal(callbackErrorKey('provider_internal_stack_trace'), 'generic')
    assert.equal(callbackErrorKey(null), 'generic')
})
