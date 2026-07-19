'use client'

import {Button} from '@/components/ui/button'
import {cn} from '@/lib/utils'
import {useTranslation} from 'react-i18next'

export function QueryErrorState({
                                  message,
                                  retrying,
                                  onRetry,
                                  className,
                                }: {
  message: string
  retrying: boolean
  onRetry: () => void
  className?: string
}) {
  const {t} = useTranslation()

  return (
    <div
      role="alert"
      className={cn('rounded-xl border border-border bg-card p-5 text-sm', className)}
    >
      <p className="text-muted-foreground">{message}</p>
      <Button
        variant="outline"
        size="sm"
        className="mt-3 whitespace-normal"
        disabled={retrying}
        onClick={onRetry}
      >
        {retrying ? t('common.retrying') : t('common.tryAgain')}
      </Button>
    </div>
  )
}
