import type {DepositBlockReason, DepositReadiness} from '@/lib/types/api'

export type DepositGateState = 'allowed' | DepositBlockReason

/**
 * Resolves what the deposit affordance should offer, from the server's
 * pre-flight answer alone.
 *
 * The reason is never re-derived here from `kyc_level` / `custody_status`: the
 * API owns the gate (it is the same evaluation `POST /wallet/deposits`
 * enforces), and a second copy of that rule in the client is a copy that will
 * drift. An absent or unrecognised readiness falls open to 'allowed' — the
 * request still enforces every gate, and a failed probe must never take the
 * deposit button away from a fully-onboarded user.
 */
export function resolveDepositGate(readiness?: DepositReadiness): DepositGateState {
  if (!readiness || readiness.allowed) return 'allowed'
  switch (readiness.blocked_by) {
    case 'kyc':
    case 'custody_absent':
    case 'custody_pending':
    case 'custody_blocked':
      return readiness.blocked_by
    default:
      return 'allowed'
  }
}
