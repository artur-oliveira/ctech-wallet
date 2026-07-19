import assert from 'node:assert/strict'
import test from 'node:test'

import {WALLET_GAMING_TERMS_URL, WALLET_TERMS_URL} from './legal.ts'

test('wallet legal links are owned by CTech Accounts', () => {
    assert.equal(WALLET_TERMS_URL, 'https://accounts.aoctech.app/products/wallet')
    assert.equal(WALLET_GAMING_TERMS_URL, 'https://accounts.aoctech.app/products/wallet-gaming')
})
