import type {Metadata} from 'next'
import {DEFAULT_LOCALE, ENGLISH_LOCALE, type SupportedLocale} from '@/lib/locale'

const SITE_NAME = 'CTech Wallet'

export function localizedMetadata({
                                      locale,
                                      path,
                                      title,
                                      description,
                                      absoluteTitle = false,
                                  }: {
    locale: SupportedLocale
    path: string
    title: string
    description: string
    absoluteTitle?: boolean
}): Metadata {
    const canonical = `/${locale}${path}`
    return {
        title: absoluteTitle ? {absolute: title} : title,
        description,
        alternates: {
            canonical,
            languages: {
                [DEFAULT_LOCALE]: `/${DEFAULT_LOCALE}${path}`,
                [ENGLISH_LOCALE]: `/${ENGLISH_LOCALE}${path}`,
                'x-default': path || '/',
            },
        },
        openGraph: {
            title,
            description,
            url: canonical,
            siteName: SITE_NAME,
            locale: locale === ENGLISH_LOCALE ? 'en_US' : 'pt_BR',
            alternateLocale: locale === ENGLISH_LOCALE ? ['pt_BR'] : ['en_US'],
            type: 'website',
        },
        twitter: {
            card: 'summary_large_image',
            title,
            description,
        },
    }
}
