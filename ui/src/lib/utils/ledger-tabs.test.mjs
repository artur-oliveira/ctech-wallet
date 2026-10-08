import assert from 'node:assert/strict'
import test from 'node:test'

import {nextLedgerTab} from './ledger-tabs.ts'

const tabs = ['real', 'game', 'sandbox']

test('ledger tab arrow navigation wraps in both directions', () => {
    assert.equal(nextLedgerTab(tabs, 'real', 'ArrowRight'), 'game')
    assert.equal(nextLedgerTab(tabs, 'sandbox', 'ArrowRight'), 'real')
    assert.equal(nextLedgerTab(tabs, 'real', 'ArrowLeft'), 'sandbox')
})

test('ledger tab Home and End keys move to the boundaries', () => {
    assert.equal(nextLedgerTab(tabs, 'game', 'Home'), 'real')
    assert.equal(nextLedgerTab(tabs, 'game', 'End'), 'sandbox')
})

const withPurchases = ['real', 'game', 'sandbox', 'purchases']

test('purchases participates in arrow, Home and End navigation', () => {
    assert.equal(nextLedgerTab(withPurchases, 'sandbox', 'ArrowRight'), 'purchases')
    assert.equal(nextLedgerTab(withPurchases, 'purchases', 'ArrowRight'), 'real')
    assert.equal(nextLedgerTab(withPurchases, 'game', 'End'), 'purchases')
})

test('ledger tabs leave unrelated keyboard input to the browser', () => {
    assert.equal(nextLedgerTab(tabs, 'real', 'Tab'), null)
})
