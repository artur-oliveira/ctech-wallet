import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import test from 'node:test'

const dashboardSource = await readFile(new URL('../../app/dashboard/page.tsx', import.meta.url), 'utf8')
const i18nSource = await readFile(new URL('../../lib/i18n.ts', import.meta.url), 'utf8')
const manifest = JSON.parse(await readFile(new URL('../../../public/site.webmanifest', import.meta.url), 'utf8'))

test('dashboard dialogs and the non-default locale are split from initial code', () => {
    assert.match(dashboardSource, /dynamic\(\(\) => import\('@\/components\/wallet\/amount-dialog'\)/)
    assert.match(i18nSource, /await import\('@\/locales\/en\.json'\)/)
    assert.doesNotMatch(i18nSource, /^import en from/m)
})

test('install manifest has a stable wallet identity', () => {
    assert.equal(manifest.id, '/')
    assert.equal(manifest.name, 'CTech Wallet')
    assert.equal(manifest.short_name, 'CTech Wallet')
    assert.equal(manifest.start_url, '/')
})
