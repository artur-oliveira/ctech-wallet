import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import test from 'node:test'

const dashboardSource = await readFile(new URL('../../app/dashboard/page.tsx', import.meta.url), 'utf8')
const historySource = await readFile(new URL('../../components/wallet/purchase-history.tsx', import.meta.url), 'utf8')
const clientSource = await readFile(new URL('../api/client.ts', import.meta.url), 'utf8')

test('purchase records render in their own history rather than the wallet statement', () => {
  assert.match(dashboardSource, /<PurchaseHistory\s*\/>/)
  assert.match(historySource, /queryKey: \['purchases', 'sandbox'\]/)
  assert.match(historySource, /queryKey: \['purchases', 'product'\]/)
  assert.match(historySource, /purchases\.kind\./)
})

test('both purchase histories use ownership-scoped user routes with pagination', () => {
  assert.match(clientSource, /SANDBOX_PURCHASES_PATH/)
  assert.match(clientSource, /PRODUCT_PURCHASES_PATH/)
  assert.match(historySource, /fetchNextPage/)
})
