'use client'

import {useEffect} from 'react'
import {useRouter} from 'next/navigation'
import {Wallet as WalletIcon} from 'lucide-react'
import {useAuth} from '@/lib/hooks/useAuth'
import {Button} from '@/components/ui/button'

export default function LoginPage() {
  const {authenticated, loading, login} = useAuth()
  const router = useRouter()
  
  useEffect(() => {
    if (!loading && authenticated) router.replace('/dashboard')
  }, [loading, authenticated, router])
  
  return (
    <div className="flex min-h-screen items-center justify-center bg-gradient-login p-6">
      <div className="w-full max-w-sm rounded-2xl border border-brand-100 bg-white p-8 text-center shadow-card">
        <div className="mx-auto flex size-12 items-center justify-center rounded-xl bg-brand-600 text-white">
          <WalletIcon size={22}/>
        </div>
        <h1 className="mt-4 text-2xl font-bold text-gray-900">CTech Wallet</h1>
        <p className="mt-2 text-sm leading-relaxed text-gray-600">
          Sua carteira digital: saldo real via PIX e créditos para jogar.
        </p>
        <Button variant="brand" size="lg" className="mt-6 w-full" onClick={login} disabled={loading}>
          Entrar com a conta CTech
        </Button>
      </div>
    </div>
  )
}
