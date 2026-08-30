import assert from 'node:assert/strict'
import test from 'node:test'

import {resolveDepositGate} from './deposit-gate.ts'

const ready = {allowed: true, kyc_level: 'enhanced', custody_required: false}

test('an allowed readiness offers the plain deposit action', () => {
    assert.equal(resolveDepositGate(ready), 'allowed')
})

test('a blocked readiness offers exactly the reason the server reported', () => {
    for (const reason of [
        'kyc', 'custody_absent', 'custody_fee_pending', 'custody_documents', 'custody_pending', 'custody_blocked',
    ]) {
        assert.equal(
            resolveDepositGate({allowed: false, blocked_by: reason, kyc_level: '', custody_required: true}),
            reason,
        )
    }
})

test('the gate falls open when the server could not answer', () => {
    // A failed /me read, or an older API, must never take deposits away from a
    // user who is perfectly onboarded — the request itself still enforces it.
    assert.equal(resolveDepositGate(undefined), 'allowed')
    assert.equal(
        resolveDepositGate({allowed: false, blocked_by: 'something_new', kyc_level: 'enhanced', custody_required: true}),
        'allowed',
    )
})

test('the reason is never re-derived from kyc_level or custody_status', () => {
    // allowed:true wins even when the raw fields look like a block: one place
    // decides, and it is the server.
    assert.equal(
        resolveDepositGate({allowed: true, kyc_level: '', custody_required: true, custody_status: 'pending_approval'}),
        'allowed',
    )
})
