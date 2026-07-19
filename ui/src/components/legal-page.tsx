'use client'

import {useEffect, type ReactNode} from 'react'
import {Wallet} from 'lucide-react'
import {LanguageSwitcher} from '@/components/language-switcher'

export function LegalPage({
                              title,
                              updatedAt,
                              updatedLabel,
                              metaDescription,
                              children,
                          }: {
    title: string
    updatedAt: string
    updatedLabel: string
    metaDescription: string
    children: ReactNode
}) {
    useEffect(() => {
        document.title = `${title} | CTech Wallet`
        const description = document.querySelector<HTMLMetaElement>('meta[name="description"]')
        description?.setAttribute('content', metaDescription)
    }, [metaDescription, title])

    return (
        <div className="min-h-screen bg-background">
            <div className="mx-auto max-w-3xl px-6 py-12">
                <div className="flex items-center justify-between gap-4">
                    <div className="flex items-center gap-2.5">
                        <div className="flex size-8 items-center justify-center rounded-lg bg-brand-600 text-white">
                            <Wallet size={16}/>
                        </div>
                        <span className="font-semibold text-foreground">CTech Wallet</span>
                    </div>
                    <LanguageSwitcher/>
                </div>

                <h1 className="mt-6 text-2xl font-bold tracking-tight text-foreground">{title}</h1>
                <p className="mt-1 text-sm text-muted-foreground">{updatedLabel} {updatedAt}</p>

                <article className="mt-8 space-y-8 text-sm leading-relaxed text-foreground">{children}</article>
            </div>
        </div>
    )
}

export function LegalSection({heading, children}: { heading: string; children: ReactNode }) {
    return (
        <section className="space-y-3">
            <h2 className="text-base font-semibold tracking-tight text-foreground">{heading}</h2>
            <div className="space-y-3">{children}</div>
        </section>
    )
}
