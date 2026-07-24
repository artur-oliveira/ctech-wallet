'use client'

import {useState} from 'react'
import {useRouter} from 'next/navigation'
import {useMutation, useQueryClient} from '@tanstack/react-query'
import {toast} from 'sonner'
import {Dice5} from 'lucide-react'
import {useTranslation} from 'react-i18next'
import {apiClient, ApiError} from '@/lib/api/client'
import {ProtectedRoute} from '@/components/protected-route'
import {Button} from '@/components/ui/button'
import {WALLET_GAMING_TERMS_URL} from '@/lib/legal'
import {formatCredits, MAX_AMOUNT_DIGITS} from '@/lib/utils/money'
import type {GameLimitsInput} from '@/lib/types/api'
import {LanguageSwitcher} from '@/components/language-switcher'

function activationMessage(err: unknown, t: (k: string) => string): string {
  if (!(err instanceof ApiError)) return t('gambling.error.generic')
  switch (err.type) {
    case '/problems/kyc-not-verified':
      return t('gambling.error.kycNotVerified')
    case '/problems/gambling-terms-required':
      return t('gambling.error.termsRequired')
    default:
      return err.detail || t('gambling.error.generic')
  }
}

/**
 * Opt-in to the game + sandbox wallets.
 *
 * The checkbox starts unchecked and the button stays disabled until it is ticked:
 * a pre-ticked box is not consent, and this is the consent the whole ring-fence
 * rests on. Activation is recorded in an append-only audit log.
 */
function ActivateGamblingInner() {
  const {t} = useTranslation()
  const router = useRouter()
  const qc = useQueryClient()
  const [checked, setChecked] = useState(false)
  const [limits, setLimits] = useState<GameLimitsInput>({daily_limit: 0, weekly_limit: 0, monthly_limit: 0})
  const coherent = limits.daily_limit > 0 && limits.daily_limit <= limits.weekly_limit && limits.weekly_limit <= limits.monthly_limit

  const activate = useMutation({
    mutationFn: () => apiClient.activateGambling(limits),
    onSuccess: () => {
      void qc.invalidateQueries({queryKey: ['balances']})
      toast.success(t('toast.gamblingActivated'))
      router.push('/dashboard')
    },
    onError: (err) => toast.error(activationMessage(err, t)),
  })

  return (
    <div className="flex min-h-screen items-center justify-center bg-background px-4 py-4">
      <div className="w-full max-w-md space-y-5 rounded-2xl border border-brand-100 bg-card p-6">
        <div className="flex items-center justify-between">
          <div className="flex size-10 items-center justify-center rounded-lg bg-brand-600 text-white">
            <Dice5 aria-hidden="true" size={20}/>
          </div>
          <LanguageSwitcher/>
        </div>

        <div>
          <h1 className="text-lg font-semibold text-foreground">{t('gambling.title')}</h1>
          <p className="mt-1 text-sm leading-relaxed text-muted-foreground">
            {t('gambling.description')}
          </p>
        </div>

        <ul className="space-y-2 rounded-xl bg-muted p-4 text-sm leading-relaxed text-muted-foreground">
          <li>{t('gambling.bullet1')}</li>
          <li>{t('gambling.bullet2')}</li>
          <li>{t('gambling.bullet3')}</li>
        </ul>

        <div>
          <h2 className="text-sm font-semibold text-foreground">{t('gambling.limitsTitle')}</h2>
          <p className="mt-1 text-xs text-muted-foreground">{t('gambling.limitsDescription')}</p>
          <div className="mt-3 grid gap-3 sm:grid-cols-3">
            {(['daily', 'weekly', 'monthly'] as const).map((window) => {
              const key = `${window}_limit` as keyof GameLimitsInput
              return <label key={window} className="text-xs font-medium text-foreground">
                {t(`responsible.limits.${window}`)}
                <div
                  className="mt-1 flex items-center gap-1 rounded-lg border border-border px-2 focus-within:border-brand-500 focus-within:ring-3 focus-within:ring-brand-500/20">
                  <span className="text-muted-foreground">R$</span>
                  <input
                    inputMode="decimal"
                    value={formatCredits(limits[key])}
                    onChange={(event) => {
                      const digits = event.target.value.replace(/\D/g, '').slice(0, MAX_AMOUNT_DIGITS)
                      setLimits({...limits, [key]: Number.parseInt(digits || '0', 10)})
                    }}
                    className="h-9 min-w-0 w-full bg-transparent font-mono outline-none"
                  />
                </div>
              </label>
            })}
          </div>
          {!coherent && <p className="mt-2 text-xs text-destructive">{t('responsible.limits.coherence')}</p>}
        </div>

        <label className="flex min-h-11 cursor-pointer items-start gap-2 text-sm text-muted-foreground">
          <input
            type="checkbox"
            checked={checked}
            onChange={(e) => setChecked(e.target.checked)}
            className="mt-0.5 size-4 shrink-0 rounded border-border accent-brand-600 focus-visible:outline-none focus-visible:ring-3 focus-visible:ring-brand-500/30"
          />
          <span>
            {t('gambling.checkboxPrefix')}{' '}
            <a
              href={WALLET_GAMING_TERMS_URL}
              target="_blank"
              rel="noreferrer"
              className="text-foreground underline underline-offset-4"
            >
              {t('gambling.termsLink')}
            </a>
            .
          </span>
        </label>

        <div className="flex flex-col-reverse gap-2 sm:flex-row">
          <Button variant="outline" className="w-full sm:flex-1" onClick={() => router.push('/dashboard')}>
            {t('gambling.later')}
          </Button>
          <Button
            variant="brand"
            className="w-full sm:flex-1"
            disabled={!checked || !coherent || activate.isPending}
            onClick={() => activate.mutate()}
          >
            {activate.isPending ? t('gambling.activating') : t('gambling.activate')}
          </Button>
        </div>
      </div>
    </div>
  )
}

export default function ActivateGamblingPage() {
  return (
    <ProtectedRoute>
      <ActivateGamblingInner/>
    </ProtectedRoute>
  )
}
