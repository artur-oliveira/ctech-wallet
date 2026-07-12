'use client'

import {useState} from 'react'
import {useMutation, useQuery, useQueryClient} from '@tanstack/react-query'
import {toast} from 'sonner'
import {LogOut} from 'lucide-react'
import {apiClient, ApiError} from '@/lib/api/client'
import {formatBRL} from '@/lib/utils/money'
import {useAuth} from '@/lib/hooks/useAuth'
import {ProtectedRoute} from '@/components/protected-route'
import {BalanceCards} from '@/components/wallet/balance-cards'
import {LedgerList} from '@/components/wallet/ledger-list'
import {AmountDialog} from '@/components/wallet/amount-dialog'
import {PixChargeDialog} from '@/components/wallet/pix-charge-dialog'
import {Button} from '@/components/ui/button'
import type {DepositResult, WalletType} from '@/lib/types/api'
import Image from 'next/image';

type Flow = 'deposit' | 'withdraw' | 'credits' | 'fund-game' | 'return-game' | null

const LEDGER_TAB_LABEL: Record<WalletType, string> = {
  real: 'Extrato real',
  game: 'Extrato de jogo',
  sandbox: 'Extrato sandbox',
}

/** Game and sandbox statements exist only once the user has activated gambling. */
function ledgerTabs(activated: boolean): WalletType[] {
  return activated ? ['real', 'game', 'sandbox'] : ['real']
}

/** Turns an RFC 7807 problem from the API into copy the user can act on. */
function problemMessage(err: unknown): string {
  if (!(err instanceof ApiError)) return 'Algo deu errado. Tente de novo.'
  switch (err.type) {
    case '/problems/insufficient-balance':
      return 'Saldo insuficiente para essa operação.'
    case '/problems/wallet-busy':
      return 'Já existe uma operação em andamento. Aguarde alguns segundos.'
    case '/problems/withdraw-cpf-mismatch':
      return 'A chave PIX pertence a outro CPF. Use uma chave no seu nome.'
    case '/problems/pix-key-not-found':
      return 'Chave PIX não encontrada. Confira e tente de novo.'
    case '/problems/kyc-not-verified':
      return 'Verifique sua identidade na sua conta antes de continuar.'
    case '/problems/step-up-required':
      return 'Confirme sua identidade com MFA e tente o saque de novo.'
    case '/problems/idempotency-conflict':
      return 'Essa operação já foi enviada com outros dados. Recarregue a página.'
    case '/problems/gambling-not-activated':
      return 'Ative a carteira de jogo para usar essa operação.'
    case '/problems/gambling-terms-required':
      return 'Aceite o termo de jogo responsável para continuar.'
    case '/problems/deposit-out-of-range': {
      const {min_amount: min, max_amount: max} = (err.raw ?? {}) as {min_amount?: number; max_amount?: number}
      if (min == null || max == null) return err.detail
      return `O depósito precisa ser entre ${formatBRL(min)} e ${formatBRL(max)}.`
    }
    default:
      return err.detail
  }
}

/** A fresh idempotency key per submit attempt — replays are safe server-side. */
function newIdemKey(): string {
  return crypto.randomUUID()
}

function DashboardInner() {
  const {profile, logout} = useAuth()
  const qc = useQueryClient()
  const [flow, setFlow] = useState<Flow>(null)
  const [charge, setCharge] = useState<DepositResult | null>(null)
  const [tab, setTab] = useState<WalletType>('real')

  const balances = useQuery({queryKey: ['balances'], queryFn: () => apiClient.getBalances()})

  function refresh() {
    void qc.invalidateQueries({queryKey: ['balances']})
    void qc.invalidateQueries({queryKey: ['ledger']})
  }

  const deposit = useMutation({
    mutationFn: (amount: number) => apiClient.createDeposit(amount),
    onSuccess: (result) => {
      setFlow(null)
      setCharge(result)
    },
    onError: (err) => toast.error(problemMessage(err)),
  })

  const withdraw = useMutation({
    mutationFn: ({amount, pixKey}: { amount: number; pixKey: string }) =>
      apiClient.createWithdrawal(amount, pixKey, newIdemKey()),
    onSuccess: (w) => {
      setFlow(null)
      refresh()
      toast.success(
        w.status === 'processing'
          ? 'Saque em processamento. O PIX cai em instantes.'
          : 'Saque enviado.',
      )
    },
    onError: (err) => toast.error(problemMessage(err)),
  })

  const buyCredits = useMutation({
    mutationFn: (amount: number) => apiClient.purchaseSandbox(amount, newIdemKey()),
    onSuccess: () => {
      setFlow(null)
      refresh()
      toast.success('Créditos adicionados.')
    },
    onError: (err) => toast.error(problemMessage(err)),
  })

  const fundGame = useMutation({
    mutationFn: (amount: number) => apiClient.fundGame(amount, newIdemKey()),
    onSuccess: () => {
      setFlow(null)
      refresh()
      toast.success('Saldo enviado para a carteira de jogo.')
    },
    onError: (err) => toast.error(problemMessage(err)),
  })

  const returnFromGame = useMutation({
    mutationFn: (amount: number) => apiClient.returnFromGame(amount, newIdemKey()),
    onSuccess: () => {
      setFlow(null)
      refresh()
      toast.success('Saldo devolvido ao saldo real.')
    },
    onError: (err) => toast.error(problemMessage(err)),
  })

  const name = profile?.first_name ?? profile?.username ?? ''

  return (
    <div className="min-h-screen bg-gray-50">
      <header className="border-b border-gray-200 bg-white">
        <div className="mx-auto flex max-w-4xl items-center justify-between px-6 py-4">
          <div className="flex items-center gap-2.5">
            <div className="flex size-8 items-center justify-center rounded-lg bg-brand-600 text-white">
              <Image src="/app.svg"
                     alt="Wallet"
                     width={32}
                     height={32}/>
            </div>
            <span className="font-semibold text-gray-900">CTech Wallet</span>
          </div>
          <div className="flex items-center gap-3">
            {name && <span className="hidden text-sm text-gray-500 sm:inline">{name}</span>}
            <Button variant="ghost" size="icon-sm" onClick={logout} aria-label="Sair">
              <LogOut size={16}/>
            </Button>
          </div>
        </div>
      </header>

      <main className="mx-auto max-w-4xl space-y-6 px-6 py-8">
        {balances.isLoading && (
          <div className="h-44 animate-pulse rounded-2xl bg-gray-200" aria-label="Carregando saldos"/>
        )}

        {balances.error && (
          <p className="rounded-xl border border-gray-200 bg-white p-5 text-sm text-gray-600">
            Não foi possível carregar seus saldos. Atualize a página para tentar de novo.
          </p>
        )}

        {balances.data && (
          <>
            <BalanceCards
              balances={balances.data}
              onDeposit={() => setFlow('deposit')}
              onWithdraw={() => setFlow('withdraw')}
              onBuyCredits={() => setFlow('credits')}
              onFundGame={() => setFlow('fund-game')}
              onReturnFromGame={() => setFlow('return-game')}
            />

            <section className="overflow-hidden rounded-xl border border-gray-200 bg-white">
              <div className="flex border-b border-gray-100">
                {ledgerTabs(balances.data.activated).map((t) => (
                  <button
                    key={t}
                    onClick={() => setTab(t)}
                    className={`px-5 py-3.5 text-xs font-semibold uppercase tracking-wider transition-colors ${
                      tab === t
                        ? 'border-b-2 border-brand-600 text-brand-700'
                        : 'text-gray-400 hover:text-gray-600'
                    }`}
                  >
                    {LEDGER_TAB_LABEL[t]}
                  </button>
                ))}
              </div>
              <LedgerList type={tab}/>
            </section>
          </>
        )}
      </main>

      {flow === 'deposit' && (
        <AmountDialog
          title="Depositar via PIX"
          description="Geramos um código PIX para você pagar no app do seu banco."
          submitLabel="Gerar código"
          pending={deposit.isPending}
          onSubmit={(amount) => deposit.mutate(amount)}
          onClose={() => setFlow(null)}
        />
      )}

      {flow === 'withdraw' && (
        <AmountDialog
          title="Sacar para o PIX"
          description="A taxa de saque é descontada do seu saldo junto com o valor."
          submitLabel="Sacar"
          withPixKey
          pending={withdraw.isPending}
          onSubmit={(amount, pixKey) => withdraw.mutate({amount, pixKey: pixKey!})}
          onClose={() => setFlow(null)}
        />
      )}

      {flow === 'credits' && (
        <AmountDialog
          title="Comprar créditos sandbox"
          description="Debita do seu saldo de jogo. Créditos sandbox não voltam a virar dinheiro."
          submitLabel="Comprar"
          pending={buyCredits.isPending}
          onSubmit={(amount) => buyCredits.mutate(amount)}
          onClose={() => setFlow(null)}
        />
      )}

      {flow === 'fund-game' && (
        <AmountDialog
          title="Enviar para a carteira de jogo"
          description="Continua sendo seu dinheiro: você pode devolvê-lo ao saldo real quando quiser. Seus limites de jogo valem aqui."
          submitLabel="Enviar"
          pending={fundGame.isPending}
          onSubmit={(amount) => fundGame.mutate(amount)}
          onClose={() => setFlow(null)}
        />
      )}

      {flow === 'return-game' && (
        <AmountDialog
          title="Devolver ao saldo real"
          description="Sem taxa e sem limite — devolver dinheiro para fora da carteira de jogo é sempre permitido."
          submitLabel="Devolver"
          pending={returnFromGame.isPending}
          onSubmit={(amount) => returnFromGame.mutate(amount)}
          onClose={() => setFlow(null)}
        />
      )}

      {charge && <PixChargeDialog deposit={charge} onClose={() => setCharge(null)}/>}
    </div>
  )
}

export default function DashboardPage() {
  return (
    <ProtectedRoute>
      <DashboardInner/>
    </ProtectedRoute>
  )
}
