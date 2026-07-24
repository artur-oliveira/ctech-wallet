export const DEFAULT_LOCALE = 'pt-BR'
export const ENGLISH_LOCALE = 'en'
export const LOCALE_STORAGE_KEY = 'i18nextLng'
export const LOCALE_COOKIE_NAME = 'wallet_locale'
export const LOCALE_COOKIE_MAX_AGE_SECONDS = 31_536_000

export type SupportedLocale = typeof DEFAULT_LOCALE | typeof ENGLISH_LOCALE

export function normalizeLocale(value: string | null | undefined): SupportedLocale {
  return value?.toLowerCase().startsWith('en') ? ENGLISH_LOCALE : DEFAULT_LOCALE
}

export function localeFromPath(pathname: string): SupportedLocale | null {
  if (pathname === `/${ENGLISH_LOCALE}` || pathname.startsWith(`/${ENGLISH_LOCALE}/`)) {
    return ENGLISH_LOCALE
  }
  if (pathname === `/${DEFAULT_LOCALE}` || pathname.startsWith(`/${DEFAULT_LOCALE}/`)) {
    return DEFAULT_LOCALE
  }
  return null
}

export function localizedPath(pathname: string, locale: SupportedLocale): string {
  const current = localeFromPath(pathname)
  if (!current) return `/${locale}${pathname === '/' ? '' : pathname}`
  const suffix = pathname.slice(current.length + 1)
  return `/${locale}${suffix}`
}

export function persistLocalePreference(locale: SupportedLocale): void {
  window.localStorage.setItem(LOCALE_STORAGE_KEY, locale)
  document.cookie = `${LOCALE_COOKIE_NAME}=${encodeURIComponent(locale)}; Max-Age=${LOCALE_COOKIE_MAX_AGE_SECONDS}; Path=/; SameSite=Lax`
}
