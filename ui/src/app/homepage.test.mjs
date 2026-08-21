import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import test from 'node:test'

// The markup lives in a client component so `/` can own its hreflang
// alternates: a page that exports `metadata` cannot be a client component.
const pageSource = readFileSync(new URL('../components/home.tsx', import.meta.url), 'utf8')
const portuguese = JSON.parse(
    readFileSync(new URL('../locales/pt-BR.json', import.meta.url), 'utf8'),
)
const english = JSON.parse(
    readFileSync(new URL('../locales/en.json', import.meta.url), 'utf8'),
)
const rootPageSource = readFileSync(new URL('./page.tsx', import.meta.url), 'utf8')

test('the homepage is one direct product gateway, not a landing-page template', () => {
    assert.doesNotMatch(pageSource, /FEATURE_KEYS|FEATURE_ICONS|Sparkles|Zap/)
    assert.doesNotMatch(pageSource, /home\.hero|home\.features|\.map\(/)
    assert.equal(pageSource.match(/<Button\b/g)?.length, 1)
    assert.match(pageSource, /<LanguageSwitcher\s*\/>/)
    assert.match(pageSource, /login\(DASHBOARD_PATH\)/)
    assert.match(pageSource, /authenticated \? openDashboard : loginToDashboard/)

    for (const locale of [portuguese, english]) {
        assert.equal('hero' in locale.home, false)
        assert.equal('features' in locale.home, false)
        assert.equal(typeof locale.home.title, 'string')
        assert.equal(typeof locale.home.description, 'string')
    }
})

test('the unprefixed homepage reciprocates the locale cluster it is the x-default of', () => {
    // Without this, /en and /pt-BR name `/` as x-default and `/` names nobody
    // back, so a crawler drops the whole annotation. `/` only became a served
    // page when the CloudFront locale redirect went away.
    assert.match(rootPageSource, /alternates: ROOT_ALTERNATES/)
    assert.match(rootPageSource, /export \{default\} from '@\/components\/home'/)
    assert.doesNotMatch(rootPageSource, /'use client'/)
})
