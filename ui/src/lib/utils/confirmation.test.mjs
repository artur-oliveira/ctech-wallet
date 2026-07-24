import assert from 'node:assert/strict'
import {describe, it} from 'node:test'
import {matchesConfirmationPhrase} from './confirmation.ts'

describe('matchesConfirmationPhrase', () => {
    it('accepts the localized phrase without requiring a specific letter case', () => {
        assert.equal(matchesConfirmationPhrase('exclude', 'EXCLUDE', 'en'), true)
        assert.equal(matchesConfirmationPhrase('excluir', 'EXCLUIR', 'pt-BR'), true)
    })

    it('ignores accidental surrounding whitespace but rejects a different phrase', () => {
        assert.equal(matchesConfirmationPhrase('  EXCLUIR  ', 'EXCLUIR', 'pt-BR'), true)
        assert.equal(matchesConfirmationPhrase('CONFIRMAR', 'EXCLUIR', 'pt-BR'), false)
    })
})
