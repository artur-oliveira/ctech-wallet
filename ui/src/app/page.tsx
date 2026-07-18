'use client'

import Link from 'next/link'
import { useTranslation } from 'react-i18next'
import { Sparkles, Wallet, Zap, ShieldCheck } from 'lucide-react'
import { useAuth } from '@/lib/hooks/useAuth'
import { Button } from '@/components/ui/button'

const FEATURE_ICONS = [Wallet, Zap, ShieldCheck, Sparkles] as const
const FEATURE_KEYS = ['free', 'pix', 'segregated', 'future'] as const

export default function Home() {
    const { t } = useTranslation()
    const { authenticated } = useAuth()

    const ctaHref = authenticated ? '/dashboard' : '/login'
    const ctaLabel = authenticated ? t('home.cta.dashboard') : t('home.cta.login')

    return (
        <div className="min-h-screen bg-background">
            <header className="mx-auto flex max-w-5xl items-center justify-between px-6 py-6">
                <div className="flex items-center gap-2.5">
                    <div className="flex size-8 items-center justify-center rounded-lg bg-brand-600 text-white">
                        <Wallet size={16} />
                    </div>
                    <span className="font-semibold text-foreground">CTech Wallet</span>
                </div>
                <Button variant="brand" render={<Link href={ctaHref} />}>{ctaLabel}</Button>
            </header>

            <section className="mx-auto max-w-3xl px-6 py-16 text-center md:py-24">
                <p className="font-mono text-xs tracking-widest text-brand-600 uppercase">{t('home.hero.eyebrow')}</p>
                <h1 className="mt-4 text-4xl font-bold leading-tight tracking-tight text-foreground md:text-5xl">
                    {t('home.hero.title')}
                </h1>
                <p className="mx-auto mt-4 max-w-xl text-base leading-relaxed text-muted-foreground">
                    {t('home.hero.subtitle')}
                </p>
                <div className="mt-8 flex justify-center">
                    <Button variant="brand" size="lg" render={<Link href={ctaHref} />}>
                        {ctaLabel}
                    </Button>
                </div>
            </section>

            <section className="mx-auto max-w-5xl px-6 py-16">
                <h2 className="mb-8 text-center text-2xl font-bold text-foreground md:text-3xl">
                    {t('home.features.title')}
                </h2>
                <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                    {FEATURE_KEYS.map((key, i) => {
                        const Icon = FEATURE_ICONS[i]
                        return (
                            <div key={key} className="rounded-xl bg-card p-5 ring-1 ring-foreground/10">
                                <div className="flex size-10 items-center justify-center rounded-lg bg-brand-600/10 text-brand-600">
                                    <Icon size={20} />
                                </div>
                                <p className="mt-3 font-semibold text-foreground">{t(`home.features.${key}.title`)}</p>
                                <p className="mt-1.5 text-sm leading-relaxed text-muted-foreground">
                                    {t(`home.features.${key}.body`)}
                                </p>
                            </div>
                        )
                    })}
                </div>
            </section>

            <footer className="border-t border-border">
                <div className="mx-auto flex max-w-5xl flex-col items-center gap-3 px-6 py-8 text-sm text-muted-foreground md:flex-row md:justify-between">
                    <p>© {new Date().getFullYear()} A O CARVALHO TECH</p>
                    <div className="flex items-center gap-4">
                        <a href="https://accounts.aoctech.app/terms" className="hover:text-foreground" target="_blank" rel="noreferrer">
                            {t('home.footer.terms')}
                        </a>
                        <a href="https://accounts.aoctech.app/privacy" className="hover:text-foreground" target="_blank" rel="noreferrer">
                            {t('home.footer.privacy')}
                        </a>
                    </div>
                </div>
            </footer>
        </div>
    )
}
