'use client'

import {useEffect, useState, type ReactNode} from 'react'
import {createInstance, type ResourceLanguage} from 'i18next'
import {I18nextProvider, initReactI18next} from 'react-i18next'
import {persistLocalePreference, type SupportedLocale} from '@/lib/locale'

export function StaticLocaleBoundary({
                                         locale,
                                         resources,
                                         children,
                                     }: {
    locale: SupportedLocale
    resources: ResourceLanguage
    children: ReactNode
}) {
    const [instance] = useState(() => {
        const localized = createInstance()
        void localized.use(initReactI18next).init({
            resources: {[locale]: {translation: resources}},
            lng: locale,
            fallbackLng: locale,
            supportedLngs: [locale],
            initAsync: false,
            interpolation: {escapeValue: false},
        })
        return localized
    })

    useEffect(() => {
        document.documentElement.lang = locale
        persistLocalePreference(locale)
    }, [locale])

    return (
        <I18nextProvider i18n={instance}>
            <div lang={locale}>{children}</div>
        </I18nextProvider>
    )
}
