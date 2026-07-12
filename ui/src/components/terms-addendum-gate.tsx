'use client'

import {useState} from 'react'
import {useMutation, useQueryClient} from '@tanstack/react-query'
import {ShieldCheck} from 'lucide-react'
import {apiClient} from '@/lib/api/client'
import {Button} from '@/components/ui/button'

/**
 * Blocks the whole app until the user accepts the wallet's terms addendum.
 *
 * The wallet custodies real money and holds a non-convertible sandbox balance —
 * neither is covered by the account's master terms, and an SSO sign-up never
 * presents a checkbox for product-specific terms. Acceptance is checked against
 * the current version, so bumping it re-gates everyone.
 */
export function TermsAddendumGate() {
  const qc = useQueryClient()
  const [checked, setChecked] = useState(false)

  const accept = useMutation({
    mutationFn: () => apiClient.acceptTermsAddendum(),
    onSuccess: () => qc.invalidateQueries({queryKey: ['me']}),
  })

  return (
    <div className="flex min-h-screen items-center justify-center bg-gradient-login px-4">
      <div className="w-full max-w-md space-y-5 rounded-2xl border border-brand-100 bg-white p-6 shadow-card">
        <div className="flex size-10 items-center justify-center rounded-lg bg-brand-600 text-white">
          <ShieldCheck size={20} />
        </div>

        <div>
          <h1 className="text-lg font-semibold text-gray-900">Só mais um passo</h1>
          <p className="mt-1 text-sm leading-relaxed text-gray-600">
            A carteira movimenta dinheiro real e créditos sandbox. Antes de continuar, confirme que você leu os
            termos específicos da CTech Wallet.
          </p>
        </div>

        {accept.isError && (
          <p className="text-sm text-red-600">Não foi possível confirmar. Tente de novo.</p>
        )}

        <label className="flex items-start gap-2 text-sm text-gray-600">
          <input
            type="checkbox"
            checked={checked}
            onChange={(e) => setChecked(e.target.checked)}
            className="mt-0.5 size-4 shrink-0 rounded border-gray-300 accent-brand-600"
          />
          <span>
            Li e concordo com os{' '}
            <a
              href="/terms-addendum"
              target="_blank"
              rel="noreferrer"
              className="text-gray-900 underline underline-offset-4"
            >
              Termos Adicionais da CTech Wallet
            </a>
            .
          </span>
        </label>

        <Button
          variant="brand"
          className="w-full"
          disabled={!checked || accept.isPending}
          onClick={() => accept.mutate()}
        >
          {accept.isPending ? 'Confirmando…' : 'Continuar'}
        </Button>
      </div>
    </div>
  )
}
