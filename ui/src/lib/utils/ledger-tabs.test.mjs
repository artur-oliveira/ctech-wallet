import assert from 'node:assert/strict'
import test from 'node:test'

import {availableLedgerTabs, nextLedgerTab} from './ledger-tabs.ts'

const tabs = ['real', 'sandbox', 'purchases']

test('ledger tab arrow navigation wraps in both directions', () => {
    assert.equal(nextLedgerTab(tabs, 'real', 'ArrowRight'), 'sandbox')
    assert.equal(nextLedgerTab(tabs, 'purchases', 'ArrowRight'), 'real')
    assert.equal(nextLedgerTab(tabs, 'real', 'ArrowLeft'), 'purchases')
})

test('ledger tab Home and End keys move to the boundaries', () => {
    assert.equal(nextLedgerTab(tabs, 'sandbox', 'Home'), 'real')
    assert.equal(nextLedgerTab(tabs, 'sandbox', 'End'), 'purchases')
})

test('ledger tabs leave unrelated keyboard input to the browser', () => {
    assert.equal(nextLedgerTab(tabs, 'real', 'Tab'), null)
})

test('the Sandbox statement is offered only while a Sandbox wallet exists, and there is no game statement', () => {
    assert.deepEqual(availableLedgerTabs(true), ['real', 'sandbox', 'purchases'])
    assert.deepEqual(availableLedgerTabs(false), ['real', 'purchases'])
})
