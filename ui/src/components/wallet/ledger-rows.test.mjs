import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import test from 'node:test'

const listSource = await readFile(new URL('./ledger-list.tsx', import.meta.url), 'utf8')
const typesSource = await readFile(new URL('../../lib/types/api.ts', import.meta.url), 'utf8')
const moneySource = await readFile(new URL('../../lib/utils/money.ts', import.meta.url), 'utf8')

test('ledger rows show balance before and after from the server', () => {
  assert.match(typesSource, /balance_before: number/)
  assert.match(listSource, /entry\.balance_before/)
  assert.match(listSource, /entry\.balance_after/)
  assert.match(listSource, /ledger\.balance\.before/)
  assert.match(listSource, /ledger\.balance\.after/)
})

test('balances use the money formatter for money wallets and bare credits for sandbox', () => {
  assert.match(moneySource, /export function formatBalance\(amount: number, monetary: boolean/)
  assert.match(moneySource, /monetary \? formatBRL\(amount, locale\) : formatCreditsAmount\(amount, locale\)/)
})

test('the statement loads by infinite scroll instead of a load-more button', () => {
  assert.match(listSource, /useInfiniteSentinel/)
  assert.equal(/dashboard\.ledger\.loadMore['"`]/.test(listSource), false)
})

test('a failed next page keeps the rows and offers a retry', () => {
  assert.match(listSource, /isFetchNextPageError/)
  assert.match(listSource, /common\.tryAgain/)
})
