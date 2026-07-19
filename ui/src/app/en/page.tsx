import {localizedMetadata} from '@/lib/localized-metadata'
import {ENGLISH_LOCALE} from '@/lib/locale'
import en from '@/locales/en.json'
import {Metadata} from "next";

export const metadata: Metadata = localizedMetadata({
    locale: ENGLISH_LOCALE,
    path: '',
    title: 'CTech Wallet',
    description: en.home.description,
    absoluteTitle: true,
})

export {default} from '@/app/page'
