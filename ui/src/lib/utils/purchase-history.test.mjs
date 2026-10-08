import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import test from 'node:test'

const dashboardSource = await readFile(new URL('../../app/dashboard/page.tsx', import.meta.url), 'utf8')
const tabsSource = await readFile(new URL('../../components/wallet/ledger-tabs.tsx', import.meta.url), 'utf8')
const panelSource = await readFile(new URL('../../components/wallet/purchases-panel.tsx', import.meta.url), 'utf8')
const clientSource = await readFile(new URL('../api/client.ts', import.meta.url), 'utf8')

test('purchases live in the statement tabs, not as a separate dashboard section', () => {
  assert.equal(/<PurchaseHistory\s*\/>/.test(dashboardSource), false)
  assert.match(tabsSource, /<PurchasesPanel\s*\/>/)
})

test('sandbox credits and digital products stay separate lists with their own queries', () => {
  assert.match(panelSource, /queryKey: \['purchases', kind\]/)
  assert.match(panelSource, /apiClient\.getSandboxPurchases/)
  assert.match(panelSource, /apiClient\.getProductPurchases/)
  assert.match(panelSource, /purchases\.kind\./)
  assert.match(panelSource, /useInfiniteSentinel/)
})

test('both purchase lists use ownership-scoped user routes with pagination', () => {
  assert.match(clientSource, /SANDBOX_PURCHASES_PATH/)
  assert.match(clientSource, /PRODUCT_PURCHASES_PATH/)
  assert.match(panelSource, /fetchNextPage/)
})
