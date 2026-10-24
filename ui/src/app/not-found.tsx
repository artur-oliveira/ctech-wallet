'use client'

import Link from 'next/link'
import {ErrorState} from '@aoctech/ui'
import {useTranslation} from 'react-i18next'
import {Button} from '@aoctech/ui'
import {normalizeLocale} from '@/lib/locale'
import {useAuth} from '@/lib/hooks/useAuth'

export default function NotFound() {
  const {authenticated} = useAuth()
  const {t, i18n} = useTranslation()

  return (
    <ErrorState
      status={404}
      locale={normalizeLocale(i18n.language)}
      title={t('notFound.header')}
      description={`${t('notFound.description')} ${t('systemState.reassurance')}`}
      action={
        <Button variant="brand"  render={<Link href={authenticated ? '/dashboard' : '/login'}/>}>
          {authenticated ? t('notFound.backToAccount') : t('notFound.backToLogin')}
        </Button>
      }
    />
  )
}
