'use client'

import {Languages} from 'lucide-react'
import {useTranslation} from 'react-i18next'
import {Button} from '@/components/ui/button'
import {normalizeLocale} from '@/lib/locale'

export function LanguageSwitcher() {
    const {i18n, t} = useTranslation()
    const current = normalizeLocale(i18n.language)
    const next = current === 'en' ? 'pt-BR' : 'en'
    const nextLabel = next === 'en' ? 'EN' : 'PT'
    const nextLanguageName = next === 'en' ? 'English' : 'Português'

    return (
        <Button
            variant="ghost"
            size="sm"
            onClick={() => void i18n.changeLanguage(next)}
            aria-label={t('common.changeLanguage', {language: nextLanguageName})}
        >
            <Languages aria-hidden="true"/>
            <span className="text-xs font-medium uppercase">{nextLabel}</span>
        </Button>
    )
}
