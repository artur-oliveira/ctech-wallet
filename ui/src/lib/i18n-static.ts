import ptBR from '@/locales/pt-BR.json'

// Legal pages (terms/gambling addenda) are Server Components rendering static
// pt-BR prose (see I18nProvider's isStaticPage comment) — they must not import
// '@/lib/i18n', which initializes react-i18next and calls createContext at
// module scope, which Server Components cannot do.
function get(key: string): string {
    const value = key
        .split('.')
        .reduce<unknown>(
            (acc, k) => (acc && typeof acc === 'object' ? (acc as Record<string, unknown>)[k] : undefined),
            ptBR,
        )
    return typeof value === 'string' ? value : key
}

const i18n = {t: get}
export default i18n
