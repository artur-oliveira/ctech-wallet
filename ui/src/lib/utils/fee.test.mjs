import assert from 'node:assert/strict'
import {describe, it} from 'node:test'
import {
    FEE_ABSOLUTE_MIN,
    maxWithdrawable,
    withdrawalFee,
} from './fee.ts'

describe('withdrawalFee', () => {
    it('uses the documented defaults at the minimum, percentage, and maximum boundaries', () => {
        assert.equal(withdrawalFee(100), 100)
        assert.equal(withdrawalFee(10_000), 200)
        assert.equal(withdrawalFee(100_000), 1_000)
    })

    it('uses positive per-wallet overrides', () => {
        const feeConfig = {fee_bps: 100, fee_min: 200, fee_max: 5_000}

        assert.equal(withdrawalFee(10_000, feeConfig), 200)
        assert.equal(withdrawalFee(600_000, feeConfig), 5_000)
    })

    it('falls back for unset overrides and never drops below the absolute floor', () => {
        assert.equal(withdrawalFee(5_000, {fee_bps: 0, fee_min: 0, fee_max: 0}), 100)
        assert.equal(
            withdrawalFee(5_000, {fee_bps: 100, fee_min: 10, fee_max: 5_000}),
            FEE_ABSOLUTE_MIN,
        )
    })

    it('widens an incoherent maximum to the effective minimum', () => {
        assert.equal(withdrawalFee(100_000, {fee_min: 500, fee_max: 200}), 500)
    })
})

describe('maxWithdrawable', () => {
    it('returns the largest amount whose amount plus fee fits the balance', () => {
        assert.equal(maxWithdrawable(10_000), 9_804)
        assert.equal(withdrawalFee(9_804) + 9_804, 10_000)
    })

    it('uses wallet overrides when finding the maximum', () => {
        const feeConfig = {fee_bps: 1_000, fee_min: 100, fee_max: 10_000}

        assert.equal(maxWithdrawable(10_000, feeConfig), 9_091)
        assert.equal(withdrawalFee(9_091, feeConfig) + 9_091, 10_000)
    })

    it('returns zero when the balance cannot cover the absolute fee floor', () => {
        assert.equal(maxWithdrawable(FEE_ABSOLUTE_MIN - 1), 0)
        assert.equal(maxWithdrawable(400, {fee_min: 500}), 0)
    })
})
