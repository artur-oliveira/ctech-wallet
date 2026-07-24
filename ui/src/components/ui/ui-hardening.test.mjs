import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import test from 'node:test'

const dashboardSource = await readFile(new URL('../../app/dashboard/page.tsx', import.meta.url), 'utf8')
const ledgerSource = await readFile(new URL('../wallet/ledger-list.tsx', import.meta.url), 'utf8')
const responsibleSource = await readFile(new URL('../../app/gambling/responsible/page.tsx', import.meta.url), 'utf8')
const callbackSource = await readFile(new URL('../../app/callback/page.tsx', import.meta.url), 'utf8')

test('primary query failures share an in-context retry state', () => {
    for (const source of [dashboardSource, ledgerSource, responsibleSource]) {
        assert.match(source, /<QueryErrorState/)
        assert.match(source, /refetch\(\)/)
    }
})

test('OAuth callback never displays raw provider or exception text', () => {
    assert.match(callbackSource, /callbackErrorKey\(errorParam\)/)
    assert.match(callbackSource, /catch \{/)
    assert.doesNotMatch(callbackSource, /errorParam\}\s*$/m)
    assert.doesNotMatch(callbackSource, /err\.message/)
    assert.doesNotMatch(callbackSource, /errorPrefix/)
})
