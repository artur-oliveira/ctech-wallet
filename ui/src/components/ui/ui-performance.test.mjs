import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import test from 'node:test'

const dashboardSource = await readFile(new URL('../../app/dashboard/page.tsx', import.meta.url), 'utf8')
const pixDialogSource = await readFile(new URL('../wallet/pix-charge-dialog.tsx', import.meta.url), 'utf8')
const i18nSource = await readFile(new URL('../../lib/i18n.ts', import.meta.url), 'utf8')
const liveStatusSource = await readFile(new URL('../wallet/transaction-status-list.tsx', import.meta.url), 'utf8')
const manifest = JSON.parse(await readFile(new URL('../../../public/site.webmanifest', import.meta.url), 'utf8'))

test('transaction fallback uses one bounded, WebSocket-aware ledger query', () => {
    assert.match(dashboardSource, /TRANSACTION_LEDGER_LIMIT = 50/)
    assert.match(dashboardSource, /wsStatus === 'connected'/)
    assert.match(dashboardSource, /refetchIntervalInBackground: false/)
    assert.doesNotMatch(dashboardSource, /getLedger\('real', undefined, 200\)/)
    assert.doesNotMatch(pixDialogSource, /getLedger|refetchInterval|useQuery/)
})

test('dashboard dialogs and the non-default locale are split from initial code', () => {
    assert.match(dashboardSource, /dynamic\(\(\) => import\('@\/components\/wallet\/amount-dialog'\)/)
    assert.match(dashboardSource, /dynamic\(\(\) => import\('@\/components\/wallet\/pix-charge-dialog'\)/)
    assert.match(i18nSource, /await import\('@\/locales\/en\.json'\)/)
    assert.doesNotMatch(i18nSource, /^import en from/m)
})

test('transaction updates use one dedicated live region instead of every badge', () => {
    assert.equal(liveStatusSource.match(/role="status"/g)?.length, 1)
    assert.match(liveStatusSource, /previousStatuses/)
    assert.match(liveStatusSource, /aria-atomic="true"/)
})

test('install manifest has a stable wallet identity', () => {
    assert.equal(manifest.id, '/')
    assert.equal(manifest.name, 'CTech Wallet')
    assert.equal(manifest.short_name, 'CTech Wallet')
    assert.equal(manifest.start_url, '/')
})
