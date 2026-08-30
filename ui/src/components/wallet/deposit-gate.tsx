'use client'

import {ArrowDownToLine, Clock, FileText, IdCard, LifeBuoy, Mail, Receipt, Wallet} from 'lucide-react'
import {useTranslation} from 'react-i18next'
import {Button} from '@/components/ui/button'
import {ACCOUNT_IDENTITY_URL, ACCOUNT_SUPPORT_URL} from '@/lib/legal'
import type {DepositBlockReason, DepositReadiness} from '@/lib/types/api'

/** Ties the gate button to its explanation for assistive tech. */
const NOTE_ID = 'deposit-gate-note'
import {resolveDepositGate} from '@/lib/utils/deposit-gate'

interface DepositGateProps {
  /** Undefined means the readiness probe is unknown — behave as allowed. */
  readiness?: DepositReadiness
  onDeposit: () => void
  onOpenCustody: () => void
  /** Reopens the outstanding verification-fee charge. */
  onPayCustodyFee: () => void
  /**
   * Provider-hosted document upload, when the provider asked for one. Documents
   * that carry this link cannot be sent any other way, so without it there is
   * nothing for the user to act on and the state reads as "under review".
   */
  onboardingURL?: string
}

/**
 * The deposit affordance on the real card.
 *
 * A user must never type an amount into a flow that is going to be refused: the
 * button IS the next step. When the gate is unknown (the probe failed, or an
 * older API) it falls open to the plain deposit button — the request itself
 * still enforces every gate, and a transient read must never take the deposit
 * away from someone who is fully onboarded.
 */
export function DepositGate({readiness, onDeposit, onOpenCustody, onPayCustodyFee, onboardingURL}: DepositGateProps) {
  const {t} = useTranslation()
  const state = resolveDepositGate(readiness)

  if (state === 'allowed') {
    return (
      <Button variant="secondary" className="bg-white text-brand-700 hover:bg-brand-50" onClick={onDeposit}>
        <ArrowDownToLine size={16}/>
        {t('balance.deposit')}
      </Button>
    )
  }

  switch (state) {
    case 'kyc':
      return (
        <Button
          variant="secondary"
          className="bg-white text-brand-700 hover:bg-brand-50"
          aria-describedby={NOTE_ID}
          render={<a href={ACCOUNT_IDENTITY_URL}/>}
        >
          <IdCard size={16}/>
          {t('deposit.gate.kyc.action')}
        </Button>
      )
    case 'custody_absent':
      return (
        <Button
          variant="secondary"
          className="bg-white text-brand-700 hover:bg-brand-50"
          aria-describedby={NOTE_ID}
          onClick={onOpenCustody}
        >
          <Wallet size={16}/>
          {t('deposit.gate.custodyAbsent.action')}
        </Button>
      )
    case 'custody_fee_pending':
      return (
        <Button
          variant="secondary"
          className="bg-white text-brand-700 hover:bg-brand-50"
          aria-describedby={NOTE_ID}
          onClick={onPayCustodyFee}
        >
          <Receipt size={16}/>
          {t('deposit.gate.custodyFeePending.action')}
        </Button>
      )
    case 'custody_documents':
      // The provider usually emails the document steps to the account holder
      // rather than handing back a link. With no link there is nothing to open,
      // so the button points at the inbox instead of being disabled — the user
      // does have an action, it just is not on this screen.
      if (!onboardingURL) {
        return (
          <Button variant="secondary" className="bg-white text-brand-700" aria-describedby={NOTE_ID} disabled>
            <Mail size={16}/>
            {t('deposit.gate.custodyEmail.action')}
          </Button>
        )
      }
      return (
        <Button
          variant="secondary"
          className="bg-white text-brand-700 hover:bg-brand-50"
          aria-describedby={NOTE_ID}
          render={<a href={onboardingURL} target="_blank" rel="noopener noreferrer"/>}
        >
          <FileText size={16}/>
          {t('deposit.gate.custodyDocuments.action')}
        </Button>
      )
    case 'custody_pending':
      return (
        <Button variant="secondary" className="bg-white text-brand-700" aria-describedby={NOTE_ID} disabled>
          <Clock size={16}/>
          {t('deposit.gate.custodyPending.action')}
        </Button>
      )
    case 'custody_blocked':
      return (
        <Button
          variant="outline"
          className="border-brand-400/60 bg-transparent text-white hover:bg-brand-700"
          aria-describedby={NOTE_ID}
          render={<a href={ACCOUNT_SUPPORT_URL}/>}
        >
          <LifeBuoy size={16}/>
          {t('deposit.gate.custodyBlocked.action')}
        </Button>
      )
  }
}

/** The one-line explanation under the card actions. Nothing when unblocked. */
export function DepositGateNote({readiness, onboardingURL, pendingDocuments}: {
  readiness?: DepositReadiness
  onboardingURL?: string
  pendingDocuments?: string[]
}) {
  const {t} = useTranslation()
  const state = resolveDepositGate(readiness)
  if (state === 'allowed') return null

  if (state === 'custody_documents') {
    // Naming the outstanding documents is the whole point when there is no link
    // to send the user to: "under review" alone leaves them with no idea what
    // is missing or where it was asked for.
    const list = pendingDocuments?.length ? pendingDocuments.join(', ') : undefined
    return (
      <p id={NOTE_ID} className="mt-3 text-sm text-brand-50">
        {onboardingURL
          ? t('deposit.gate.custodyDocuments.note')
          : t('deposit.gate.custodyEmail.note')}
        {list && ` ${t('deposit.gate.custodyDocuments.list', {documents: list})}`}
      </p>
    )
  }

  const key: Record<DepositBlockReason, string> = {
    kyc: 'deposit.gate.kyc.note',
    custody_absent: 'deposit.gate.custodyAbsent.note',
    custody_fee_pending: 'deposit.gate.custodyFeePending.note',
    custody_documents: 'deposit.gate.custodyDocuments.note',
    custody_pending: 'deposit.gate.custodyPending.note',
    custody_blocked: 'deposit.gate.custodyBlocked.note',
  }
  return <p id={NOTE_ID} className="mt-3 text-sm text-brand-50">{t(key[state])}</p>
}
