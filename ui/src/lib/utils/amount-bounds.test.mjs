import assert from 'node:assert/strict'
import test from 'node:test'

import {amountBounds} from './amount-bounds.ts'

const NONE = Number.POSITIVE_INFINITY

test('server limits tighten the static ceiling', () => {
  // deposit: ring-fence ceiling R$1.000.000, but today only R$400 are left
  assert.deepEqual(amountBounds({ceiling: 100_000_000, balance: NONE, limitNow: 40_000, min: 100}),
    {min: 100, max: 40_000, blocked: false})
})

test('the balance still caps a withdrawal below the daily limit', () => {
  assert.deepEqual(amountBounds({ceiling: NONE, balance: 30_000, limitNow: 100_000, min: 100}),
    {min: 100, max: 30_000, blocked: false})
})

test('a used-up daily limit blocks the flow', () => {
  assert.equal(amountBounds({ceiling: NONE, balance: 50_000, limitNow: 0, min: 100}).blocked, true)
})

test('without server limits the old behaviour is kept', () => {
  assert.deepEqual(amountBounds({ceiling: 100_000_000, balance: NONE, min: undefined}),
    {min: 1, max: 100_000_000, blocked: false})
})

test('the minimum never exceeds the maximum (full-balance withdrawal below the minimum)', () => {
  assert.deepEqual(amountBounds({ceiling: NONE, balance: 50, limitNow: 50, min: 100}),
    {min: 50, max: 50, blocked: false})
})
