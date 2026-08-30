'use client'

import Link from 'next/link'
import type {ReactNode} from 'react'
import {House, LifeBuoy, RefreshCw, SearchX, ShieldAlert, Wrench} from 'lucide-react'
import {Button} from '@/components/ui/button'

/**
 * The one treatment for every dead-end screen: not found, crashed, unreachable.
 *
 * Three near-identical pages is how vocabularies drift, and on a money surface a
 * subtly different error page reads as a different failure. So the code, the
 * readout label, the icon and the reassurance line all come from one table here,
 * and each route supplies only its own copy.
 *
 * The ghost numeral is the wallet's existing 404 treatment, kept deliberately:
 * the instrument reports its state plainly and large, and nothing about a
 * failure earns Signal Violet — violet means "this is the action", so the only
 * violet on the page is the button that recovers from it.
 */
export type SystemStateCode = '404' | '500' | '503'

const ICONS = {'404': SearchX, '500': ShieldAlert, '503': Wrench} as const

interface SystemStateProps {
  code: SystemStateCode
  /** Short mono readout naming the state, e.g. "PÁGINA NÃO ENCONTRADA". */
  readout: string
  title: string
  description: string
  /** One line on what the user can do, or what we are doing. */
  detail?: string
  /** Renders the retry button when the state is actually retryable. */
  onRetry?: () => void
  retryLabel?: string
  /** Disables retry and swaps its label while a check is in flight. */
  retryPending?: boolean
  /** Primary navigation escape. */
  homeHref: string
  homeLabel: string
  /** The money-safety line. Always shown: it is the first thing a user wonders. */
  reassurance: string
  /** Optional extra action, e.g. a support link. */
  children?: ReactNode
}

export function SystemState({
                              code,
                              readout,
                              title,
                              description,
                              detail,
                              onRetry,
                              retryLabel,
                              retryPending,
                              homeHref,
                              homeLabel,
                              reassurance,
                              children,
                            }: SystemStateProps) {
  const Icon = ICONS[code]

  return (
    <main className="flex min-h-screen flex-col items-center justify-center px-6 py-16">
      <div className="w-full max-w-md">
        {/* Numeral, then icon-beside-name: the icon labels the state, so it
            belongs on the line that names it, not floating beside digits four
            times its size. */}
        <p
          aria-hidden="true"
          className="font-mono text-6xl leading-none font-semibold tabular-nums text-foreground/12 select-none sm:text-7xl"
        >
          {code}
        </p>

        <p className="mt-4 flex items-center gap-2 font-mono text-xs font-semibold tracking-[0.08em] text-muted-foreground uppercase">
          <Icon size={14} className="shrink-0" aria-hidden="true"/>
          {readout}
        </p>

        <h1 className="mt-2 text-xl font-semibold text-balance text-foreground">{title}</h1>

        <p className="mt-2 text-sm text-pretty text-muted-foreground">{description}</p>

        {detail && (
          <p aria-live="polite" className="mt-3 text-sm text-pretty text-muted-foreground/90">{detail}</p>
        )}

        <div className="mt-7 flex flex-col gap-2 sm:flex-row">
          {onRetry && (
            <Button
              size="lg"
              variant="brand"
              className="w-full sm:w-auto"
              onClick={onRetry}
              disabled={retryPending}
            >
              <RefreshCw size={16} className={retryPending ? 'motion-safe:animate-spin' : undefined}/>
              {retryLabel}
            </Button>
          )}
          <Button
            size="lg"
            variant={onRetry ? 'outline' : 'brand'}
            className="w-full sm:w-auto"
            render={<Link href={homeHref}/>}
          >
            <House size={16}/>
            {homeLabel}
          </Button>
          {children}
        </div>

        {/* Hairline, not a card: the reassurance is a footnote to the state
            above it, and boxing it would make it look like a separate alert. */}
        <p className="mt-8 border-t border-border pt-4 text-xs text-muted-foreground">
          {reassurance}
        </p>
      </div>
    </main>
  )
}

/** The support escape hatch, for states no retry can clear. */
export function SystemStateSupport({href, label}: { href: string; label: string }) {
  return (
    <Button size="lg" variant="ghost" className="w-full sm:w-auto" render={<a href={href}/>}>
      <LifeBuoy size={16}/>
      {label}
    </Button>
  )
}
