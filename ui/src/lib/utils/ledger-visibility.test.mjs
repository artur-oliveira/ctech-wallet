import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import test from 'node:test'
import {availableLedgerTabs} from './ledger-tabs.ts'

const tabsSource = await readFile(new URL('../../components/wallet/ledger-tabs.tsx', import.meta.url), 'utf8')
const listSource = await readFile(new URL('../../components/wallet/ledger-list.tsx', import.meta.url), 'utf8')
const dashboardSource = await readFile(new URL('../../app/dashboard/page.tsx', import.meta.url), 'utf8')

test('an existing sandbox statement stays visible as history', () => {
  assert.deepEqual(availableLedgerTabs(true), ['real', 'sandbox', 'purchases'])
  assert.match(tabsSource, /availableLedgerTabs\(hasSandbox\)/)
  assert.match(dashboardSource, /hasSandbox=\{!!balances\.data\.sandbox\}/)
  assert.doesNotMatch(dashboardSource, /hasGame|activated=/)
})

test('sandbox PIX credits are presented as purchases from their stable reference prefix', () => {
  assert.match(listSource, /SANDBOX_PURCHASE_REFERENCE_PREFIX = 'sbxp'/)
  assert.match(listSource, /return 'sandbox_direct_purchase'/)
})
