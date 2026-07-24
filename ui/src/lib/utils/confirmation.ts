export function matchesConfirmationPhrase(value: string, phrase: string, locale: string): boolean {
  return value.trim().toLocaleUpperCase(locale) === phrase.toLocaleUpperCase(locale)
}
