import assert from 'node:assert/strict'
import test from 'node:test'

import {
    applyRealtimeStatus,
    parseStoredDeposit,
    parseTransactionHistory,
    reconcileTransactionHistory,
    upsertTransaction,
} from './transaction-status.ts'

const withdrawal = {
    id: 'withdraw#user#idem',
    kind: 'withdrawal',
    amount: 5_000,
    fee: 100,
    status: 'processing',
    created_at: '2026-07-19T18:00:00.000Z',
}

test('upsertTransaction keeps one authoritative row per transaction id', () => {
    const result = upsertTransaction(
        [withdrawal],
        {...withdrawal, status: 'completed', updated_at: '2026-07-19T18:01:00.000Z'},
    )

    assert.equal(result.length, 1)
    assert.equal(result[0].status, 'completed')
})

test('realtime withdrawal outcomes only update the matching server id', () => {
    const other = {...withdrawal, id: 'withdraw#user#other'}
    const result = applyRealtimeStatus(
        [withdrawal, other],
        {type: 'withdraw_reversed', transactionId: withdrawal.id},
    )

    assert.equal(result.find((item) => item.id === withdrawal.id)?.status, 'reversed')
    assert.equal(result.find((item) => item.id === other.id)?.status, 'processing')
})

test('ledger reconciliation confirms a deposit only by its exact txid', () => {
    const deposits = [
        {
            id: 'tx_expected',
            kind: 'deposit',
            amount: 10_000,
            status: 'pending',
            created_at: '2026-07-19T18:00:00.000Z',
            expires_at: 2_000_000_000
        },
        {
            id: 'tx_other',
            kind: 'deposit',
            amount: 10_000,
            status: 'pending',
            created_at: '2026-07-19T18:00:00.000Z',
            expires_at: 2_000_000_000
        },
    ]
    const result = reconcileTransactionHistory(deposits, [
        {
            entry_id: 'e1',
            wallet_id: 'w_real',
            type: 'deposit',
            amount: 10_000,
            balance_after: 20_000,
            ref: 'tx_expected',
            created_at: '2026-07-19T18:01:00.000Z'
        },
    ], 1_900_000_000_000)

    assert.equal(result.find((item) => item.id === 'tx_expected')?.status, 'confirmed')
    assert.equal(result.find((item) => item.id === 'tx_other')?.status, 'pending')
})

test('ledger reconciliation marks only the referenced withdrawal as reversed', () => {
    const other = {...withdrawal, id: 'withdraw#user#other'}
    const result = reconcileTransactionHistory([withdrawal, other], [
        {
            entry_id: 'e1',
            wallet_id: 'w_real',
            type: 'reversal',
            amount: 5_100,
            balance_after: 20_000,
            ref: `reverse:${withdrawal.id}`,
            created_at: '2026-07-19T18:01:00.000Z'
        },
    ], Date.now())

    assert.equal(result.find((item) => item.id === withdrawal.id)?.status, 'reversed')
    assert.equal(result.find((item) => item.id === other.id)?.status, 'processing')
})

test('stored transaction history rejects malformed and unknown status values', () => {
    const parsed = parseTransactionHistory(JSON.stringify([
        withdrawal,
        {...withdrawal, id: 'bad-status', status: 'made_up'},
        {...withdrawal, id: 42},
    ]))

    assert.deepEqual(parsed, [withdrawal])
})

test('stored PIX charge must retain the fields needed to resume safely', () => {
    const charge = {
        txid: 'tx_expected',
        amount: 10_000,
        status: 'pending',
        pix_copia_e_cola: '000201-mock',
        expires_at: 2_000_000_000,
    }

    assert.deepEqual(parseStoredDeposit(JSON.stringify(charge)), charge)
    assert.equal(parseStoredDeposit(JSON.stringify({...charge, pix_copia_e_cola: ''})), null)
    assert.equal(parseStoredDeposit('{broken'), null)
})
