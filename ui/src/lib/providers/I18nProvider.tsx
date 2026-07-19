'use client'

import {useEffect, useRef, useState} from 'react'
import {usePathname} from 'next/navigation'
import {I18nextProvider, useTranslation} from 'react-i18next'
import LanguageDetector from 'i18next-browser-languagedetector'
import i18n from '@/lib/i18n'
import {normalizeLocale} from '@/lib/locale'

const STORAGE_KEY = 'i18nextLng'

function LanguageSync() {
    const {i18n: instance} = useTranslation()
    const detectedOnce = useRef(false)

    useEffect(() => {
        if (detectedOnce.current) return
        detectedOnce.current = true

        const cached = window.localStorage.getItem(STORAGE_KEY)
        let resolved = cached

        if (!resolved) {
            const detector = new LanguageDetector()
            detector.init({languageUtils: instance.services.languageUtils})
            const detected = detector.detect()
            resolved = Array.isArray(detected) ? detected[0] : detected ?? null
        }

        const supported = normalizeLocale(resolved)

        if (supported !== instance.language) {
            void instance.changeLanguage(supported)
        }
    }, [instance])

    useEffect(() => {
        const locale = normalizeLocale(instance.language)
        document.documentElement.lang = locale
        window.localStorage.setItem(STORAGE_KEY, locale)
    }, [instance.language])

    return null
}

/**
 * Mirrors ctech-account's i18n provider. Client-rendered pages wait for mount
 * before rendering so `t()` never mismatches between SSR and the detected
 * browser language — that is what removes hydration warnings. Public and legal
 * pages render immediately in the default locale, then react-i18next updates
 * their copy and document language together after browser detection.
 */
export function I18nProvider({children}: { children: React.ReactNode }) {
    const [mounted, setMounted] = useState(false)
    const pathname = usePathname()

    useEffect(() => {
        const timer = setTimeout(() => setMounted(true), 0)
        return () => clearTimeout(timer)
    }, [])

    const rendersImmediately = pathname
        ? (pathname === '/' || pathname.startsWith('/terms-addendum') || pathname.startsWith('/gambling-addendum'))
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
