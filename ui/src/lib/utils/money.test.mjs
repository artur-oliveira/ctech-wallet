import assert from 'node:assert/strict'
import {describe, it} from 'node:test'
import {walletHasMonetaryValue} from './wallet-semantics.ts'

describe('wallet money semantics', () => {
    it('treats real and game balances as money, but sandbox as credits', () => {
        assert.equal(walletHasMonetaryValue('real'), true)
        assert.equal(walletHasMonetaryValue('game'), true)
        assert.equal(walletHasMonetaryValue('sandbox'), false)
    })
})
