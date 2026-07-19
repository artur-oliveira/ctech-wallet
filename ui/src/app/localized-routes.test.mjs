import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import test from 'node:test'

const englishLayout = await readFile(new URL('./en/layout.tsx', import.meta.url), 'utf8')
const portugueseLayout = await readFile(new URL('./pt-BR/layout.tsx', import.meta.url), 'utf8')
const metadataSource = await readFile(new URL('../lib/localized-metadata.ts', import.meta.url), 'utf8')
const switcherSource = await readFile(new URL('../components/language-switcher.tsx', import.meta.url), 'utf8')

test('public locale routes render with route-owned translation catalogs', () => {
    assert.match(englishLayout, /locale=\{ENGLISH_LOCALE\}/)
    assert.match(englishLayout, /resources=\{en\}/)
    assert.match(portugueseLayout, /locale=\{DEFAULT_LOCALE\}/)
    assert.match(portugueseLayout, /resources=\{ptBR\}/)
})

test('localized metadata declares canonical, alternate, and Open Graph locales', () => {
    assert.match(metadataSource, /canonical/)
    assert.match(metadataSource, /languages:/)
    assert.match(metadataSource, /alternateLocale/)
    assert.match(switcherSource, /router\.replace\(localizedPath\(pathname, next\)\)/)
})
