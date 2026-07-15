'use client'

import Link from 'next/link'
import {useTranslation} from "react-i18next";
import {Button} from "@/components/ui/button";
import {useAuth} from "@/lib/hooks/useAuth";

export default function NotFound() {
  const {authenticated} = useAuth()
  const user = authenticated
  const {t} = useTranslation()
  
  
  return (
    <div className="min-h-screen bg-background flex items-center justify-center px-4">
      <div className="text-center">
        <p className="text-7xl font-bold text-brand-500 select-none">404</p>
        <h1 className="mt-4 text-xl font-semibold text-foreground">{t('notFound.header')}</h1>
        <p className="mt-2 text-sm text-muted-foreground">
          {t('notFound.description')}
        </p>
        <Button
          variant="brand"
          className="mt-6 inline-flex items-center gap-1.5 rounded-lg px-4 py-2 text-sm font-medium text-white transition-colors"
          render={<Link href={user ? "/dashboard" : "/login"}/>}>
          {user ? t('notFound.backToAccount') : t('notFound.backToLogin')}
        </Button>
      </div>
    </div>
  )
}
