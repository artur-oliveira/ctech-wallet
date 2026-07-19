import assert from 'node:assert/strict'
import {describe, it} from 'node:test'

import {
    DEFAULT_LOCALE,
    localeFromPath,
    localizedPath,
    normalizeLocale,
} from './locale.ts'

describe('wallet locale delivery', () => {
    it('normalizes supported browser and stored language variants', () => {
        assert.equal(normalizeLocale('en-US'), 'en')
        assert.equal(normalizeLocale('en'), 'en')
        assert.equal(normalizeLocale('pt-BR'), 'pt-BR')
        assert.equal(normalizeLocale('pt'), DEFAULT_LOCALE)
        assert.equal(normalizeLocale(undefined), DEFAULT_LOCALE)
    })

    it('recognizes and switches pre-rendered locale route prefixes', () => {
        assert.equal(localeFromPath('/en'), 'en')
        assert.equal(localeFromPath('/pt-BR'), 'pt-BR')
        assert.equal(localeFromPath('/dashboard'), null)
        assert.equal(localizedPath('/en', 'pt-BR'), '/pt-BR')
        assert.equal(localizedPath('/pt-BR', 'en'), '/en')
    })
})
