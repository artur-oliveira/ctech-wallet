'use client'

import {useState} from 'react'
import {Button} from '@/components/ui/button'
import {parseCentavos} from '@/lib/utils/money'

interface AmountDialogProps {
  title: string
  description: string
  submitLabel: string
  /** Extra field for the withdrawal PIX key. */
  withPixKey?: boolean
  pending?: boolean
  onSubmit: (amount: number, pixKey?: string) => void
  onClose: () => void
}

/** Shared amount entry used by deposit, withdrawal, and credit purchase. */
export function AmountDialog({
  title,
  description,
  submitLabel,
  withPixKey,
  pending,
  onSubmit,
  onClose,
}: AmountDialogProps) {
  const [amount, setAmount] = useState('')
  const [pixKey, setPixKey] = useState('')
  const [error, setError] = useState<string | null>(null)

  function submit(e: React.FormEvent) {
    e.preventDefault()
    const centavos = parseCentavos(amount)
    if (centavos === null || centavos <= 0) {
      setError('Informe um valor válido, como 50,00.')
      return
    }
    if (withPixKey && !pixKey.trim()) {
      setError('Informe a chave PIX de destino.')
      return
    }
    setError(null)
    onSubmit(centavos, withPixKey ? pixKey.trim() : undefined)
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-gray-900/40 p-4">
      <form
        onSubmit={submit}
        className="w-full max-w-sm rounded-2xl bg-white p-6 shadow-modal"
      >
        <h2 className="text-lg font-semibold text-gray-900">{title}</h2>
        <p className="mt-1 text-sm leading-relaxed text-gray-500">{description}</p>

        <label className="mt-5 block text-sm font-medium text-gray-700" htmlFor="amount">
          Valor
        </label>
        <div className="mt-1.5 flex items-center gap-2 rounded-lg border border-gray-300 px-3 focus-within:border-brand-500 focus-within:ring-3 focus-within:ring-brand-500/20">
          <span className="text-sm text-gray-400">R$</span>
          <input
            id="amount"
            autoFocus
            inputMode="decimal"
            placeholder="0,00"
            value={amount}
            onChange={(e) => setAmount(e.target.value)}
            className="h-10 w-full border-0 bg-transparent font-mono tabular-nums outline-none"
          />
        </div>

        {withPixKey && (
          <>
            <label className="mt-4 block text-sm font-medium text-gray-700" htmlFor="pixkey">
              Chave PIX de destino
            </label>
            <input
              id="pixkey"
              placeholder="CPF, e-mail, telefone ou chave aleatória"
              value={pixKey}
              onChange={(e) => setPixKey(e.target.value)}
              className="mt-1.5 h-10 w-full rounded-lg border border-gray-300 px-3 text-sm outline-none focus:border-brand-500 focus:ring-3 focus:ring-brand-500/20"
            />
            <p className="mt-1.5 text-xs text-gray-500">
              A chave precisa estar no seu CPF. Saques para terceiros são recusados.
            </p>
          </>
        )}

        {error && <p className="mt-3 text-sm text-red-600">{error}</p>}

        <div className="mt-6 flex gap-2">
          <Button type="button" variant="ghost" className="flex-1" onClick={onClose} disabled={pending}>
            Cancelar
          </Button>
          <Button type="submit" variant="brand" className="flex-1" disabled={pending}>
            {pending ? 'Enviando...' : submitLabel}
          </Button>
        </div>
      </form>
    </div>
  )
}
