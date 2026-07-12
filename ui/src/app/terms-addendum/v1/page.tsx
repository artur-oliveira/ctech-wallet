import type {Metadata} from 'next'
import {LegalPage, LegalSection} from '@/components/legal-page'

// Keep in sync with wallet.CurrentTermsAddendumVersion in the Go API
// (api/internal/domain/wallet/user.go). The gate decision is server-side; this
// is the version the user is reading.
const ADDENDUM_VERSION = '1.0'
const UPDATED_AT = '11 de julho de 2026'

export const metadata: Metadata = {
  title: 'Termos Adicionais',
  description: 'Termos Adicionais da CTech Wallet — saldo real via PIX e créditos sandbox.',
}

export default function TermsAddendumPage() {
  return (
    <LegalPage title="Termos Adicionais — CTech Wallet" updatedAt={UPDATED_AT}>
      <p className="text-xs text-gray-400">Versão {ADDENDUM_VERSION}</p>

      <p>
        Este aditivo complementa — e não substitui — os{' '}
        <a
          href="https://accounts.aoctech.app/terms"
          className="underline underline-offset-4"
          target="_blank"
          rel="noreferrer"
        >
          Termos de Uso
        </a>{' '}
        e a{' '}
        <a
          href="https://accounts.aoctech.app/privacy"
          className="underline underline-offset-4"
          target="_blank"
          rel="noreferrer"
        >
          Política de Privacidade
        </a>{' '}
        da CTech. No que for específico da carteira digital, este aditivo prevalece.
      </p>

      <LegalSection heading="1. O que é a CTech Wallet">
        <p>
          A CTech Wallet mantém dois saldos separados na sua conta: o <strong>saldo real</strong>, movimentado por
          PIX, e os <strong>créditos sandbox</strong>, uma moeda virtual usada em aplicações integradas.
        </p>
      </LegalSection>

      <LegalSection heading="2. Quem pode usar">
        <p>
          Para movimentar saldo real você precisa ter 18 anos ou mais e concluir a verificação de identidade da sua
          conta CTech. Para sacar, a chave PIX de destino precisa pertencer ao mesmo CPF verificado na conta —
          saques para chaves de terceiros são recusados.
        </p>
      </LegalSection>

      <LegalSection heading="3. Depósitos">
        <p>
          Depósitos são recebidos por PIX. O valor entra na carteira somente após o banco parceiro confirmar o
          pagamento — nunca apenas com base em uma notificação. Se o CPF de quem pagou for diferente do CPF
          verificado na sua conta, o depósito é recusado e devolvido automaticamente a quem pagou.
        </p>
      </LegalSection>

      <LegalSection heading="4. Saques e taxa">
        <p>
          Cada saque tem uma taxa, descontada do seu saldo junto com o valor sacado. A taxa padrão é de 2% sobre o
          valor, com mínimo de R$ 1,00 e máximo de R$ 10,00 por operação, e cobre o custo da transferência PIX. A
          taxa aplicada à sua carteira é sempre exibida antes de você confirmar o saque.
        </p>
        <p>Uma carteira executa uma operação por vez: um novo saque só começa depois que o anterior é concluído.</p>
      </LegalSection>

      <LegalSection heading="5. Créditos sandbox">
        <p>
          Créditos sandbox podem ser comprados com saldo real ou concedidos por aplicações integradas. Eles servem
          para participar de partidas em jogos de habilidade integrados.
        </p>
        <p className="font-medium text-gray-900">
          Créditos sandbox não têm valor monetário, não são resgatáveis e não podem, em nenhuma hipótese, ser
          convertidos em saldo real nem sacados. A compra de créditos com saldo real é definitiva e não é
          reembolsável.
        </p>
      </LegalSection>

      <LegalSection heading="6. Limites de responsabilidade">
        <p>
          A CTech Wallet não é uma instituição financeira licenciada pelo Banco Central do Brasil. Ela atua como
          intermediário técnico de custódia e movimentação de valores via PIX, por meio de um banco parceiro. Não
          garantimos a disponibilidade ininterrupta da infraestrutura PIX de terceiros e não respondemos por
          atrasos ou falhas causados por ela.
        </p>
      </LegalSection>

      <LegalSection heading="7. Alterações">
        <p>
          Este aditivo pode ser atualizado. Alterações materiais exigem um novo aceite explícito antes de você
          continuar usando a carteira.
        </p>
      </LegalSection>

      <LegalSection heading="8. Contato">
        <p>
          A O CARVALHO TECH — CNPJ 62.787.449/0001-07. Encarregado de dados (DPO):{' '}
          <a href="mailto:dpo@aoctech.app" className="underline underline-offset-4">
            dpo@aoctech.app
          </a>
          .
        </p>
      </LegalSection>
    </LegalPage>
  )
}
