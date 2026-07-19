'use client'

import {useEffect, useRef, useState} from 'react'
import {usePathname} from 'next/navigation'
import {I18nextProvider, useTranslation} from 'react-i18next'
import LanguageDetector from 'i18next-browser-languagedetector'
import i18n, {changeAppLanguage} from '@/lib/i18n'
import {LOCALE_STORAGE_KEY, localeFromPath, normalizeLocale, persistLocalePreference} from '@/lib/locale'

function LanguageSync() {
    const {i18n: instance} = useTranslation()
    const detectedOnce = useRef(false)

    useEffect(() => {
        if (detectedOnce.current) return
        detectedOnce.current = true

        const routeLocale = localeFromPath(window.location.pathname)
        const cached = window.localStorage.getItem(LOCALE_STORAGE_KEY)
        let resolved = routeLocale ?? cached

        if (!resolved) {
            const detector = new LanguageDetector()
            detector.init({languageUtils: instance.services.languageUtils})
            const detected = detector.detect()
            resolved = Array.isArray(detected) ? detected[0] : detected ?? null
        }

        const supported = normalizeLocale(resolved)

        if (supported !== instance.language) {
            void changeAppLanguage(supported)
        }
    }, [instance])

    useEffect(() => {
        const locale = normalizeLocale(instance.language)
        document.documentElement.lang = locale
        persistLocalePreference(locale)
    }, [instance.language])

    return null
}

/**
 * Mirrors ctech-account's i18n provider. Client-rendered pages wait for mount
 * before rendering so `t()` never mismatches between SSR and the detected
 * browser language — that is what removes hydration warnings. Public locale
 * routes render immediately from their route-owned translation catalog.
 */
export function I18nProvider({children}: { children: React.ReactNode }) {
    const [mounted, setMounted] = useState(false)
    const pathname = usePathname()

    useEffect(() => {
        const timer = setTimeout(() => setMounted(true), 0)
        return () => clearTimeout(timer)
    }, [])

    const localizedPublicPath = pathname ? localeFromPath(pathname) : null
    const rendersImmediately = pathname
        ? pathname === '/' || localizedPublicPath !== null
        : false

    return (
        <I18nextProvider i18n={i18n}>
            <LanguageSync/>
            {mounted || rendersImmediately ? (
                children
            ) : (
                <div className="min-h-screen flex items-center justify-center bg-transparent" role="status">
                    <div className="animate-pulse flex flex-col items-center space-y-4">
                        <div aria-hidden="true" className="size-8 rounded-full border-2 border-primary/30 border-t-primary animate-spin"/>
                        <span className="sr-only">{i18n.t('common.loading')}</span>
                    </div>
                </div>
            )}
        </I18nextProvider>
    )
}
