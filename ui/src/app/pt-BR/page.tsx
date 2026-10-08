import {localizedMetadata} from '@/lib/localized-metadata'
import {DEFAULT_LOCALE} from '@/lib/locale'
import ptBR from '@/locales/pt-BR.json'
import {Metadata} from "next";

export const metadata: Metadata = localizedMetadata({
  locale: DEFAULT_LOCALE,
  path: '',
  title: 'CTech Wallet',
  description: ptBR.home.description,
  absoluteTitle: true,
})

export {default} from '@/app/page'
