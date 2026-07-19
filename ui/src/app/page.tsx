'use client'

import {useRouter} from 'next/navigation'
import {Wallet} from 'lucide-react'
import {useTranslation} from 'react-i18next'
import {Button} from '@/components/ui/button'
import {LanguageSwitcher} from '@/components/language-switcher'
import {useAuth} from '@/lib/hooks/useAuth'
import {ACCOUNTS_LEGAL_URL, PRIVACY_POLICY_URL, WALLET_TERMS_URL} from '@/lib/legal'

const DASHBOARD_PATH = '/dashboard'

export default function Home() {
  const {t} = useTranslation()
  const {authenticated, loading, login} = useAuth()
  const router = useRouter()

  const ctaLabel = authenticated ? t('home.cta.dashboard') : t('home.cta.login')
  const openDashboard = () => router.push(DASHBOARD_PATH)
  const loginToDashboard = () => login(DASHBOARD_PATH)

  return (
    <div className="flex min-h-screen flex-col bg-background">
      <header className="mx-auto flex w-full max-w-3xl justify-end px-6 py-5">
        <LanguageSwitcher/>
      </header>

      <main className="flex flex-1 items-center px-6 py-12">
        <section className="mx-auto w-full max-w-md">
          <div className="flex items-center gap-2.5">
            <div className="flex size-9 items-center justify-center rounded-lg bg-brand-600 text-white">
              <Wallet aria-hidden="true" size={18}/>
            </div>
            <span className="font-semibold text-foreground">CTech Wallet</span>
          </div>
          <h1 className="mt-8 text-2xl font-semibold tracking-tight text-foreground">
            {t('home.title')}
          </h1>
          <p className="mt-3 max-w-prose text-base leading-relaxed text-muted-foreground">
            {t('home.description')}
          </p>
          <Button
            variant="brand"
            size="lg"
            className="mt-8 h-auto min-h-9 w-full whitespace-normal py-2 sm:w-auto"
            onClick={authenticated ? openDashboard : loginToDashboard}
            disabled={loading}
          >
            {loading ? t('common.loading') : ctaLabel}
          </Button>
        </section>
      </main>

      <footer
        className="mx-auto flex w-full max-w-3xl flex-col gap-3 px-6 py-6 text-sm text-muted-foreground sm:flex-row sm:items-center sm:justify-between">
        <p>© {new Date().getFullYear()} A O CARVALHO TECH</p>
        <div className="flex flex-wrap gap-x-4 gap-y-2">
          <a href={WALLET_TERMS_URL} className="hover:text-foreground" target="_blank" rel="noreferrer">
            {t('home.footer.terms')}
          </a>
          <a href={PRIVACY_POLICY_URL} className="hover:text-foreground" target="_blank" rel="noreferrer">
            {t('home.footer.privacy')}
          </a>
          <a href={ACCOUNTS_LEGAL_URL} className="hover:text-foreground" target="_blank" rel="noreferrer">
            {t('home.footer.legalCenter')}
          </a>
        </div>
      </footer>
    </div>
  )
}
