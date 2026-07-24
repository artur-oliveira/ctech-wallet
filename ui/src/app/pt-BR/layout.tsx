import type {ReactNode} from 'react'
import ptBR from '@/locales/pt-BR.json'
import {StaticLocaleBoundary} from '@/components/static-locale-boundary'
import {DEFAULT_LOCALE} from '@/lib/locale'

export default function PortugueseLayout({children}: { children: ReactNode }) {
  return (
    <StaticLocaleBoundary locale={DEFAULT_LOCALE} resources={ptBR}>
      {children}
    </StaticLocaleBoundary>
  )
}
