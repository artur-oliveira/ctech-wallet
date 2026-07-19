import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import test from 'node:test'

const read = (path) => readFileSync(new URL(path, import.meta.url), 'utf8')

const globalsSource = read('../../app/globals.css')
const providerSource = read('../../lib/providers/I18nProvider.tsx')
const homeSource = read('../../app/page.tsx')
const loginSource = read('../../app/login/page.tsx')
const dashboardSource = read('../../app/dashboard/page.tsx')
const termsGateSource = read('../terms-addendum-gate.tsx')
const activationSource = read('../../app/gambling/activate/page.tsx')
const responsibleSource = read('../../app/gambling/responsible/page.tsx')
const portuguese = JSON.parse(read('../../locales/pt-BR.json'))
const english = JSON.parse(read('../../locales/en.json'))

function keyPaths(value, prefix = '') {
    return Object.entries(value).flatMap(([key, child]) => {
        const path = prefix ? `${prefix}.${key}` : key
        return child && typeof child === 'object'
            ? keyPaths(child, path)
            : [path]
    })
}

test('bare links and controls receive an explicit keyboard focus indicator', () => {
    assert.match(globalsSource, /:where\(a, button\):not\(\[data-slot="button"\]\):focus-visible/)
    assert.match(globalsSource, /outline:\s*2px solid var\(--brand-600\)/)
    assert.match(globalsSource, /outline-offset:\s*3px/)
})

test('language switching remains available across entry, consent, and wallet shells', () => {
    for (const source of [
        homeSource,
        loginSource,
        dashboardSource,
        termsGateSource,
        activationSource,
        responsibleSource,
    ]) {
        assert.match(source, /<LanguageSwitcher\s*\/>/)
    }
    assert.match(providerSource, /document\.documentElement\.lang = locale/)
    assert.deepEqual(keyPaths(english).sort(), keyPaths(portuguese).sort())
})

test('long bilingual actions can reflow at narrow widths and high zoom', () => {
    assert.match(homeSource, /whitespace-normal/)
    assert.match(loginSource, /whitespace-normal/)
    assert.match(activationSource, /flex flex-col-reverse gap-2 sm:flex-row/)
    assert.match(termsGateSource, /flex flex-col-reverse gap-2 sm:flex-row/)
})
