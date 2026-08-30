'use client'

import {useTranslation} from 'react-i18next'
import {SystemState} from '@/components/system-state'
import {useAuth} from '@/lib/hooks/useAuth'

export default function NotFound() {
  const {authenticated} = useAuth()
  const {t} = useTranslation()

  return (
    <SystemState
      code="404"
      readout={t('systemState.notFound.readout')}
      title={t('notFound.header')}
      description={t('notFound.description')}
      homeHref={authenticated ? '/dashboard' : '/login'}
      homeLabel={authenticated ? t('notFound.backToAccount') : t('notFound.backToLogin')}
      reassurance={t('systemState.reassurance')}
    />
  )
}
