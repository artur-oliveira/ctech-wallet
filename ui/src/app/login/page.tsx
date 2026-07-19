'use client'

import {useEffect} from 'react'
import {useRouter} from 'next/navigation'
import {Wallet as WalletIcon} from 'lucide-react'
import {useTranslation} from 'react-i18next'
import {useAuth} from '@/lib/hooks/useAuth'
import {Button} from '@/components/ui/button'
import {LanguageSwitcher} from '@/components/language-switcher'

export default function LoginPage() {
    const {t} = useTranslation()
    const {authenticated, loading, login} = useAuth()
    const router = useRouter()

    useEffect(() => {
        if (!loading && authenticated) router.replace('/dashboard')
    }, [loading, authenticated, router])

    return (
        <div className="flex min-h-screen items-center justify-center bg-background p-4 sm:p-6">
            <div className="w-full max-w-sm rounded-2xl border border-brand-100 bg-card p-6 text-center sm:p-8">
                <div className="flex items-center justify-between">
                    <div className="flex size-12 items-center justify-center rounded-xl bg-brand-600 text-white">
                        <WalletIcon aria-hidden="true" size={22}/>
                    </div>
                    <LanguageSwitcher/>
                </div>
                <h1 className="mt-4 text-2xl font-bold text-foreground">{t('login.title')}</h1>
                <p className="mt-2 text-sm leading-relaxed text-muted-foreground">
                    {t('login.subtitle')}
                </p>
                <Button variant="brand" size="lg" className="mt-6 h-auto min-h-9 w-full whitespace-normal py-2" onClick={() => login()}
                        disabled={loading || authenticated}>
                    {t('login.button')}
                </Button>
            </div>
        </div>
    )
}
