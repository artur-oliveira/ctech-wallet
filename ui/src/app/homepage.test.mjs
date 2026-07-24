import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import test from 'node:test'

const pageSource = readFileSync(new URL('./page.tsx', import.meta.url), 'utf8')
const portuguese = JSON.parse(
    readFileSync(new URL('../locales/pt-BR.json', import.meta.url), 'utf8'),
)
const english = JSON.parse(
    readFileSync(new URL('../locales/en.json', import.meta.url), 'utf8'),
)

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
