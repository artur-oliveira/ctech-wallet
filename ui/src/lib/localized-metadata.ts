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

/**
 * Alternates for `/`, which is not a locale route: the language is resolved in
 * the browser by `I18nProvider`, so `/` is the cluster's `x-default` rather than
 * a member of it.
 *
 * It needs its own annotation because hreflang is only honoured when every URL
 * in a set names the whole set. `/en` and `/pt-BR` have always pointed
 * `x-default` here, but `/` said nothing back — a one-way annotation a crawler
 * discards. That went unnoticed while a CloudFront Function redirected `/` to a
 * locale route so it was never served; the Cloudflare migration drops that
 * redirect and makes `/` a real page.
 *
 * Declared on the root *page* and never on the root *layout*: metadata is
 * inherited, so a canonical of `/` in the layout would also claim to be the
 * canonical of `/dashboard`, `/login`, `/callback` and `/gambling/*`.
 */
export const ROOT_ALTERNATES: Metadata['alternates'] = {
  canonical: '/',
  languages: {
    [DEFAULT_LOCALE]: `/${DEFAULT_LOCALE}`,
    [ENGLISH_LOCALE]: `/${ENGLISH_LOCALE}`,
    'x-default': '/',
  },
}
