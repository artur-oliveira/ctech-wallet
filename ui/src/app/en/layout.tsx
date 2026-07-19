import type {ReactNode} from 'react'
import en from '@/locales/en.json'
import {StaticLocaleBoundary} from '@/components/static-locale-boundary'
import {ENGLISH_LOCALE} from '@/lib/locale'

export default function EnglishLayout({children}: { children: ReactNode }) {
    return (
        <StaticLocaleBoundary locale={ENGLISH_LOCALE} resources={en}>
            {children}
        </StaticLocaleBoundary>
    )
}
