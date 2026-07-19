import i18n from 'i18next'
import {initReactI18next} from 'react-i18next'
import ptBR from '@/locales/pt-BR.json'
import {DEFAULT_LOCALE, ENGLISH_LOCALE, type SupportedLocale} from '@/lib/locale'

i18n.use(initReactI18next).init({
    resources: {
        'pt-BR': {translation: ptBR},
    },
    lng: DEFAULT_LOCALE,
    fallbackLng: DEFAULT_LOCALE,
    supportedLngs: ['en', 'pt-BR'],
    interpolation: {escapeValue: false},
})

export async function ensureLocale(locale: SupportedLocale): Promise<void> {
    if (i18n.hasResourceBundle(locale, 'translation')) return
    if (locale !== ENGLISH_LOCALE) return

    const resources = (await import('@/locales/en.json')).default
    i18n.addResourceBundle(ENGLISH_LOCALE, 'translation', resources, true, true)
}

export async function changeAppLanguage(locale: SupportedLocale): Promise<void> {
    await ensureLocale(locale)
    await i18n.changeLanguage(locale)
}

export default i18n
