import type {Metadata} from 'next'
import {LegalPage, LegalSection} from '@/components/legal-page'

// Keep in sync with wallet.CurrentGamblingAddendumVersion in the Go API
// (api/internal/domain/wallet/user.go). The gate decision is server-side; this is
// the version the user is reading.
const ADDENDUM_VERSION = '1.0'
const UPDATED_AT = '12 de julho de 2026'

export const metadata: Metadata = {
  title: 'Termo de Jogo Responsável',
  description:
    'Termo de Jogo Responsável da CTech Wallet — carteira de jogo, limites pessoais e créditos sandbox.',
}

export default function GamblingAddendumPage() {
  return (
    <LegalPage title="Termo de Jogo Responsável — CTech Wallet" updatedAt={UPDATED_AT}>
      <p className="text-xs text-gray-400">Versão {ADDENDUM_VERSION}</p>

      <p>
        Este termo é <strong>específico para o uso da carteira em jogos</strong> e complementa os{' '}
        <a href="/terms-addendum" className="underline underline-offset-4">
          Termos Adicionais da CTech Wallet
        </a>
        . Aceitar os Termos Adicionais <strong>não</strong> significa aceitar este termo: são documentos
        separados, e você só precisa deste se quiser usar a carteira para jogos.
      </p>

      <LegalSection heading="1. A carteira de jogo">
        <p>
          Ao ativar, criamos uma <strong>carteira de jogo</strong> separada do seu saldo real. O dinheiro na
          carteira de jogo <strong>continua sendo dinheiro real e seu</strong>: você pode devolvê-lo ao saldo real
          quando quiser, sem taxa e sem limite, e de lá sacar por PIX normalmente.
        </p>
        <p>
          A carteira de jogo existe para separar o que você decidiu destinar a jogos do dinheiro que usa para
          assinaturas e serviços. É a <strong>única</strong> porta pela qual dinheiro real chega aos jogos: não é
          possível jogar nem comprar créditos direto do saldo real.
        </p>
      </LegalSection>

      <LegalSection heading="2. Limites pessoais">
        <p>
          Você define <strong>quanto pode transferir do saldo real para a carteira de jogo</strong> por dia, por
          semana e por mês. O limite diário não pode ser maior que o semanal, e o semanal não pode ser maior que o
          mensal.
        </p>
        <p>
          O limite conta o <strong>total enviado</strong> no período. Devolver dinheiro da carteira de jogo para o
          saldo real <strong>não</strong> libera limite de volta — caso contrário o limite não limitaria nada.
        </p>
        <p className="font-medium text-gray-900">
          Reduzir um limite vale imediatamente. Aumentar um limite só passa a valer depois de um prazo de espera:
          7 dias para os limites diário e semanal, 14 dias para o mensal. Nunca fazemos você esperar para se
          proteger mais.
        </p>
      </LegalSection>

      <LegalSection heading="3. Créditos sandbox">
        <p>
          Créditos sandbox são comprados com o saldo da <strong>carteira de jogo</strong> e servem para participar
          de partidas em jogos de habilidade integrados.
        </p>
        <p className="font-medium text-gray-900">
          Créditos sandbox não têm valor em dinheiro, não podem ser sacados e não podem ser convertidos de volta em
          dinheiro — nem para a carteira de jogo, nem para o saldo real. A conversão é definitiva.
        </p>
      </LegalSection>

      <LegalSection heading="4. Quem pode ativar">
        <p>
          É necessário ter 18 anos ou mais e ter concluído a verificação de identidade da sua conta CTech. A
          ativação é opcional: se você usa a carteira apenas para assinaturas e serviços, não precisa dela e nada
          relacionado a jogos aparece na sua carteira.
        </p>
      </LegalSection>

      <LegalSection heading="5. Jogue com responsabilidade">
        <p>
          Jogos com dinheiro podem causar dependência. Aposte apenas o que você pode perder, e use os limites — eles
          existem para isso.
        </p>
        <p>
          Se o jogo deixou de ser diversão, procure ajuda. No Brasil, o CVV atende 24 horas pelo telefone{' '}
          <strong>188</strong> e em{' '}
          <a
            href="https://www.cvv.org.br"
            target="_blank"
            rel="noreferrer"
            className="underline underline-offset-4"
          >
            cvv.org.br
          </a>
          . Jogadores Anônimos:{' '}
          <a
            href="https://jogadoresanonimos.com.br"
            target="_blank"
            rel="noreferrer"
            className="underline underline-offset-4"
          >
            jogadoresanonimos.com.br
          </a>
          .
        </p>
      </LegalSection>
    </LegalPage>
  )
}
