'use client'

import {useState} from 'react'
import {useRouter} from 'next/navigation'
import {useMutation, useQueryClient} from '@tanstack/react-query'
import {toast} from 'sonner'
import {Dice5} from 'lucide-react'
import {apiClient, ApiError} from '@/lib/api/client'
import {ProtectedRoute} from '@/components/protected-route'
import {Button} from '@/components/ui/button'

function activationMessage(err: unknown): string {
  if (!(err instanceof ApiError)) return 'Não foi possível ativar. Tente de novo.'
  switch (err.type) {
    case '/problems/kyc-not-verified':
      return 'Verifique sua identidade na sua conta CTech antes de ativar a carteira de jogo.'
    case '/problems/gambling-terms-required':
      return 'Aceite o termo de jogo responsável para continuar.'
    default:
      return err.detail
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
  const router = useRouter()
  const qc = useQueryClient()
  const [checked, setChecked] = useState(false)

  const activate = useMutation({
    mutationFn: () => apiClient.activateGambling(),
    onSuccess: () => {
      void qc.invalidateQueries({queryKey: ['balances']})
      toast.success('Carteira de jogo ativada.')
      router.push('/dashboard')
    },
    onError: (err) => toast.error(activationMessage(err)),
  })

  return (
    <div className="flex min-h-screen items-center justify-center bg-gradient-login px-4">
      <div className="w-full max-w-md space-y-5 rounded-2xl border border-brand-100 bg-white p-6 shadow-card">
        <div className="flex size-10 items-center justify-center rounded-lg bg-brand-600 text-white">
          <Dice5 size={20} />
        </div>

        <div>
          <h1 className="text-lg font-semibold text-gray-900">Ativar a carteira de jogo</h1>
          <p className="mt-1 text-sm leading-relaxed text-gray-600">
            Criamos uma carteira separada para jogos. O dinheiro nela continua sendo seu e volta para o saldo real
            quando você quiser — sem taxa e sem limite.
          </p>
        </div>

        <ul className="space-y-2 rounded-xl bg-gray-50 p-4 text-sm leading-relaxed text-gray-600">
          <li>
            É a <strong className="font-medium text-gray-900">única</strong> porta para jogos: não dá para jogar
            direto do saldo real.
          </li>
          <li>
            Seus limites valem no envio para a carteira de jogo. Devolver dinheiro{' '}
            <strong className="font-medium text-gray-900">não</strong> libera limite de volta.
          </li>
          <li>
            Créditos sandbox não têm valor em dinheiro e{' '}
            <strong className="font-medium text-gray-900">nunca</strong> voltam a virar dinheiro.
          </li>
        </ul>

        <label className="flex items-start gap-2 text-sm text-gray-600">
          <input
            type="checkbox"
            checked={checked}
            onChange={(e) => setChecked(e.target.checked)}
            className="mt-0.5 size-4 shrink-0 rounded border-gray-300 accent-brand-600"
          />
          <span>
            Li e concordo com o{' '}
            <a
              href="/gambling-addendum"
              target="_blank"
              rel="noreferrer"
              className="text-gray-900 underline underline-offset-4"
            >
              Termo de Jogo Responsável
            </a>
            .
          </span>
        </label>

        <div className="flex gap-2">
          <Button variant="outline" className="flex-1" onClick={() => router.push('/dashboard')}>
            Agora não
          </Button>
          <Button
            variant="brand"
            className="flex-1"
            disabled={!checked || activate.isPending}
            onClick={() => activate.mutate()}
          >
            {activate.isPending ? 'Ativando…' : 'Ativar'}
          </Button>
        </div>
      </div>
    </div>
  )
}

export default function ActivateGamblingPage() {
  return (
    <ProtectedRoute>
      <ActivateGamblingInner />
    </ProtectedRoute>
  )
}
