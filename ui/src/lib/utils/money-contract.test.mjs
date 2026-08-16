import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import {dirname, join} from 'node:path'
import {fileURLToPath} from 'node:url'
import {describe, it} from 'node:test'
import {MAX_AMOUNT_CENTS, SANDBOX_CREDITS_PER_CENTAVO} from './money-constants.ts'

// Pins the ui money constants to rpc-contract/money.json, the canonical
// cross-language source (B18). rpc-contract/money_test.go is the Go mirror.
const contractPath = join(
    dirname(fileURLToPath(import.meta.url)),
    '../../../../rpc-contract/money.json',
)
const money = JSON.parse(readFileSync(contractPath, 'utf8'))

describe('money contract sync', () => {
    it('matches rpc-contract/money.json', () => {
        assert.equal(SANDBOX_CREDITS_PER_CENTAVO, money.sandbox_credits_per_centavo)
        assert.equal(MAX_AMOUNT_CENTS, money.max_amount_cents)
    })
})
