'use client'

import {useEffect} from 'react'
import {useRouter} from 'next/navigation'
import {useAuth} from '@/lib/hooks/useAuth'

/** Entry point: send the user to their wallet, or to login. */
export default function Home() {
  const {authenticated, loading} = useAuth()
  const router = useRouter()
  
  useEffect(() => {
    if (loading) return
    router.replace(authenticated ? '/dashboard' : '/login')
  }, [loading, authenticated, router])
  
  return (
    <div className="flex min-h-screen items-center justify-center bg-gradient-login">
      <div className="size-10 animate-spin rounded-full border-4 border-brand-200 border-t-brand-600"/>
    </div>
  )
}
