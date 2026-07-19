export const DEFAULT_LOCALE = 'pt-BR'
export const ENGLISH_LOCALE = 'en'

export type SupportedLocale = typeof DEFAULT_LOCALE | typeof ENGLISH_LOCALE

export function normalizeLocale(value: string | null | undefined): SupportedLocale {
    return value?.toLowerCase().startsWith('en') ? ENGLISH_LOCALE : DEFAULT_LOCALE
}
