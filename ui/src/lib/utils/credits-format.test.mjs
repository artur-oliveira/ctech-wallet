import assert from 'node:assert/strict'
import test from 'node:test'

import {CREDIT_SYMBOL, formatCreditValue} from './credits-format.ts'

const NBSP = ' '

test('the virtual currency symbol is the colon sign', () => {
  assert.equal(CREDIT_SYMBOL, '₡') // ₡
})

test('credits are whole numbers: no decimals, grouped, symbol first', () => {
  assert.equal(formatCreditValue(1000, 'pt-BR'), `₡${NBSP}1.000`)
  assert.equal(formatCreditValue(0, 'pt-BR'), `₡${NBSP}0`)
  assert.equal(formatCreditValue(200_000, 'pt-BR'), `₡${NBSP}200.000`)
  assert.equal(formatCreditValue(1000, 'en'), '₡1,000')
})

test('a stray fraction is never shown', () => {
  assert.equal(formatCreditValue(12.6, 'pt-BR'), `₡${NBSP}13`)
})

test('the sign goes before the symbol', () => {
  assert.equal(formatCreditValue(-1500, 'pt-BR', true), `−₡${NBSP}1.500`)
  assert.equal(formatCreditValue(1500, 'pt-BR', true), `+₡${NBSP}1.500`)
})

test('the symbol is rendered by the app font: layout loads the latin-ext subset that holds it', async () => {
  const {readFile} = await import('node:fs/promises')
  const layout = await readFile(new URL('../../app/layout.tsx', import.meta.url), 'utf8')
  assert.equal((layout.match(/subsets: \['latin', 'latin-ext'\]/g) ?? []).length, 2)
})
