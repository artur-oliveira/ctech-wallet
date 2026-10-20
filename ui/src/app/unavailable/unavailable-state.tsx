'use client'

import {UnavailableScreen} from '@aoctech/ui'
import {useTranslation} from 'react-i18next'
import {SESSION_KEY_RETURN_AFTER_OUTAGE} from '@/lib/constants/storage'
import {normalizeLocale} from '@/lib/locale'

const FALLBACK_DESTINATION = '/dashboard'

/**
 * The maintenance screen, reached when the API answers 503 or stops answering.
 * The NetworkProvider keeps probing with backoff and calls onRecovered on its
 * own the moment the API is back; the button only asks now.
 */
export function UnavailableState() {
  const {i18n} = useTranslation()

  const goBack = () => {
    let destination = FALLBACK_DESTINATION
    try {
      destination = window.sessionStorage.getItem(SESSION_KEY_RETURN_AFTER_OUTAGE) || destination
      window.sessionStorage.removeItem(SESSION_KEY_RETURN_AFTER_OUTAGE)
    } catch {
      // Storage unavailable: the dashboard is the safe destination.
    }
    window.location.replace(destination)
  }

  return <UnavailableScreen locale={normalizeLocale(i18n.language)} onRecovered={goBack}/>
}
