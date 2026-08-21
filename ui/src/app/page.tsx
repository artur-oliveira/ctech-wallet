import type {Metadata} from 'next'
import {ROOT_ALTERNATES} from '@/lib/localized-metadata'

// Title, description and Open Graph come from the root layout: `/` is the
// canonical presentation of the homepage, not a variant of it. Only the
// alternates are declared here — see ROOT_ALTERNATES for why they cannot live
// in the layout.
export const metadata: Metadata = {
  alternates: ROOT_ALTERNATES,
}

export {default} from '@/components/home'
