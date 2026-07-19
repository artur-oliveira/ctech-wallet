'use client'

import {Languages} from 'lucide-react'
import {usePathname, useRouter} from 'next/navigation'
import {useTranslation} from 'react-i18next'
import {Button} from '@/components/ui/button'
import {localeFromPath, localizedPath, normalizeLocale, persistLocalePreference} from '@/lib/locale'
import {changeAppLanguage} from '@/lib/i18n'

export function LanguageSwitcher() {
  const {i18n, t} = useTranslation()
  const pathname = usePathname()
  const router = useRouter()
  const current = normalizeLocale(i18n.language)
  const next = current === 'en' ? 'pt-BR' : 'en'
  const nextLabel = next === 'en' ? 'EN' : 'PT'
  const nextLanguageName = next === 'en' ? 'English' : 'Português'

  function switchLanguage() {
    persistLocalePreference(next)
    if (localeFromPath(pathname)) {
      router.replace(localizedPath(pathname, next))
      return
    }
    void changeAppLanguage(next)
  }

  return (
    <Button
      variant="ghost"
      size="sm"
      onClick={switchLanguage}
      aria-label={t('common.changeLanguage', {language: nextLanguageName})}
    >
      <Languages aria-hidden="true"/>
      <span className="text-xs font-medium uppercase">{nextLabel}</span>
    </Button>
  )
}
