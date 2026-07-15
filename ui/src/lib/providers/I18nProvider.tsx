'use client'

import {useEffect, useRef, useState} from 'react'
import {usePathname} from 'next/navigation'
import {I18nextProvider, useTranslation} from 'react-i18next'
import LanguageDetector from 'i18next-browser-languagedetector'
import i18n from '@/lib/i18n'

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

        const supported = resolved?.startsWith('en') ? 'en' : resolved === 'pt-BR' ? 'pt-BR' : null

        if (supported && supported !== instance.language) {
            instance.changeLanguage(supported)
        }
    }, [instance])

    useEffect(() => {
        document.documentElement.lang = instance.language
        window.localStorage.setItem(STORAGE_KEY, instance.language)
    }, [instance.language])

    return null
}

/**
 * Mirrors ctech-account's i18n provider. Client-rendered pages wait for mount
 * before rendering so `t()` never mismatches between SSR and the detected
 * browser language — that is what removes the hydration warnings. Static legal
 * pages (long-form pt-BR prose) render immediately.
 */
export function I18nProvider({children}: { children: React.ReactNode }) {
    const [mounted, setMounted] = useState(false)
    const pathname = usePathname()

    useEffect(() => {
        const timer = setTimeout(() => setMounted(true), 0)
        return () => clearTimeout(timer)
    }, [])

    const isStaticPage = pathname
        ? (pathname.startsWith('/terms-addendum') || pathname.startsWith('/gambling-addendum'))
        : false

    return (
        <I18nextProvider i18n={i18n}>
            <LanguageSync/>
            {mounted || isStaticPage ? (
                children
            ) : (
                <div className="min-h-screen flex items-center justify-center bg-transparent">
                    <div className="animate-pulse flex flex-col items-center space-y-4">
                        <div className="size-8 rounded-full border-2 border-primary/30 border-t-primary animate-spin"/>
                    </div>
                </div>
            )}
        </I18nextProvider>
    )
}
