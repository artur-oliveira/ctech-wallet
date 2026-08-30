import type {Metadata} from 'next'
import {UnavailableState} from './unavailable-state'

export const metadata: Metadata = {
  title: 'Serviço indisponível',
  // A transient outage must never be indexed as the wallet's content.
  robots: {index: false, follow: false},
}

export default function UnavailablePage() {
  return <UnavailableState/>
}
