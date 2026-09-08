'use client'

import Image from 'next/image'
import {useTranslation} from 'react-i18next'

const BADGE_SRC = {
  color: 'https://baas.asaas.com/selos/Servicos_financeiros_Asaas-Reduzida-Positivo.svg?id=f23b981b-3ae6-4ad7-9955-0ebbed06afc6',
  white: 'https://baas.asaas.com/selos/Servicos_financeiros_Asaas-Reduzida-Negativo-Branco.svg?id=f23b981b-3ae6-4ad7-9955-0ebbed06afc6',
} as const

interface AsaasBadgeProps {
  /** 'color' on white/light surfaces, 'white' on the filled Signal Violet card. */
  variant?: keyof typeof BADGE_SRC
  className?: string
}

/**
 * Required Asaas BaaS attribution (Resolução Conjunta nº 16/2025): wherever
 * money is moved or custodied, the end user must be told Asaas — not CTech —
 * is the regulated payment institution processing the operation. Links out
 * per Asaas's own recommended markup for the seal.
 */
export function AsaasBadge({variant = 'color', className}: AsaasBadgeProps) {
  const {t} = useTranslation()
  return (
    <a
      href="https://asaas.com"
      target="_blank"
      rel="noopener noreferrer"
      className={`inline-flex items-center ${className ?? ''}`}
    >
      <Image
        src={BADGE_SRC[variant]}
        alt={t('common.asaasBadgeAlt')}
        width={120}
        height={36}
        unoptimized
      />
    </a>
  )
}
