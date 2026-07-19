import assert from 'node:assert/strict'
import {describe, it} from 'node:test'

import {
    DEFAULT_LOCALE,
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
})
