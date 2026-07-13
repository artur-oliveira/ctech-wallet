import type {Metadata} from 'next'
import {LegalPage, LegalSection} from '@/components/legal-page'

export const metadata: Metadata = {
  title: 'Termo de Jogo Responsável',
  description:
    'Termos aplicáveis ao uso da carteira de jogo e participação em jogos integrados à plataforma CTech.',
}

const ADDENDUM_VERSION = '2.0'
const UPDATED_AT = '12 de julho de 2026'

export default function GamblingAddendumPage() {
  return (
    <LegalPage
      title="Termo de Jogo Responsável — CTech Wallet"
      updatedAt={UPDATED_AT}
    >
      <p className="text-xs text-gray-400">
        Versão {ADDENDUM_VERSION}
      </p>
      
      <p>
        Este documento complementa os{' '}
        <a
          href="/terms-addendum"
          className="underline underline-offset-4"
        >
          Termos Adicionais da CTech Wallet
        </a>{' '}
        e aplica-se exclusivamente aos usuários que optarem por ativar
        funcionalidades relacionadas a jogos.
      </p>
      
      <LegalSection heading="1. Ativação opcional">
        <p>
          O uso de funcionalidades relacionadas a jogos é totalmente
          opcional.
        </p>
        
        <p>
          Usuários que utilizarem a CTech Wallet exclusivamente para
          assinaturas, pagamentos ou demais serviços da plataforma não
          precisam ativar a carteira de jogo.
        </p>
        
        <p>
          A ativação exige:
        </p>
        
        <ul className="list-disc pl-5 space-y-2">
          <li>idade mínima de 18 (dezoito) anos;</li>
          <li>conta ativa na plataforma;</li>
          <li>verificação de identidade concluída (KYC).</li>
        </ul>
      </LegalSection>
      
      <LegalSection heading="2. Carteira de jogo">
        <p>
          A carteira de jogo constitui uma segregação lógica do saldo
          destinada exclusivamente à participação em jogos integrados à
          plataforma.
        </p>
        
        <p>
          O dinheiro mantido na carteira de jogo continua pertencendo ao
          usuário e poderá ser transferido de volta ao saldo real a qualquer
          momento, observadas restrições legais, operacionais ou de
          prevenção à fraude.
        </p>
        
        <p>
          Nenhum jogo poderá debitar diretamente o saldo real do usuário.
        </p>
        
        <p>
          Todas as transferências para a carteira de jogo exigem ação
          explícita do usuário.
        </p>
      </LegalSection>
      
      <LegalSection heading="3. Limites pessoais">
        <p>
          O usuário poderá definir limites voluntários para movimentação de
          recursos destinados a jogos, incluindo:
        </p>
        
        <ul className="list-disc pl-5 space-y-2">
          <li>limite diário;</li>
          <li>limite semanal;</li>
          <li>limite mensal.</li>
        </ul>
        
        <p>
          Os limites deverão respeitar coerência lógica entre si, não sendo
          permitido que o limite diário exceda o semanal ou que o semanal
          exceda o mensal.
        </p>
        
        <p>
          Reduções de limites produzem efeitos imediatos.
        </p>
        
        <p>
          Aumentos de limites ficam sujeitos a período de espera
          (&ldquo;cooldown&rdquo;), destinado à proteção do usuário e promoção do jogo
          responsável.
        </p>
        
        <p>
          A devolução de recursos da carteira de jogo para o saldo real não
          restabelece limites já consumidos.
        </p>
      </LegalSection>
      
      <LegalSection heading="4. Autoexclusão e pausas voluntárias">
        <p>
          O usuário poderá solicitar voluntariamente:
        </p>
        
        <ul className="list-disc pl-5 space-y-2">
          <li>bloqueio temporário;</li>
          <li>pausa preventiva;</li>
          <li>autoexclusão por prazo determinado;</li>
          <li>autoexclusão permanente.</li>
        </ul>
        
        <p>
          Durante períodos de autoexclusão, novas participações em jogos,
          transferências para a carteira de jogo e reativações antecipadas
          poderão ser impedidas.
        </p>
      </LegalSection>
      
      <LegalSection heading="5. Créditos Sandbox">
        <p>
          A plataforma poderá disponibilizar créditos sandbox para utilização
          em modalidades recreativas ou experimentais.
        </p>
        
        <p className="font-medium">
          Os créditos sandbox:
        </p>
        
        <ul className="list-disc pl-5 space-y-2">
          <li>não possuem valor econômico;</li>
          <li>não representam dinheiro;</li>
          <li>não geram direito de premiação em dinheiro;</li>
          <li>não podem ser convertidos em saldo real;</li>
          <li>não podem ser sacados;</li>
          <li>não são reembolsáveis.</li>
        </ul>
      </LegalSection>
      
      <LegalSection heading="6. Condutas proibidas">
        <p>
          É expressamente proibido:
        </p>
        
        <ul className="list-disc pl-5 space-y-2">
          <li>utilizar a plataforma sendo menor de idade;</li>
          <li>permitir utilização da conta por terceiros;</li>
          <li>compartilhar contas;</li>
          <li>utilizar bots, scripts ou automações;</li>
          <li>manter múltiplas contas sem autorização;</li>
          <li>praticar colusão entre jogadores;</li>
          <li>manipular resultados de partidas;</li>
          <li>explorar falhas técnicas para obtenção de vantagens;</li>
          <li>utilizar identidades falsas.</li>
        </ul>
      </LegalSection>
      
      <LegalSection heading="7. Medidas de segurança">
        <p>
          A CTech poderá adotar medidas para proteção da integridade das
          partidas e prevenção a fraudes, incluindo:
        </p>
        
        <ul className="list-disc pl-5 space-y-2">
          <li>verificações adicionais de identidade;</li>
          <li>análises automatizadas de comportamento;</li>
          <li>restrições preventivas;</li>
          <li>suspensão temporária de funcionalidades;</li>
          <li>retenção temporária de valores para análise.</li>
        </ul>
        
        <p>
          Havendo indícios razoáveis de fraude, abuso ou descumprimento deste
          termo, a CTech poderá bloquear contas e cancelar partidas,
          preservado o direito de defesa do usuário.
        </p>
      </LegalSection>
      
      <LegalSection heading="8. Encerramento da carteira de jogo">
        <p>
          O usuário poderá desativar a carteira de jogo a qualquer momento.
        </p>
        
        <p>
          Antes da desativação, eventuais saldos existentes deverão ser
          transferidos para o saldo real, observadas medidas de segurança e
          verificações antifraude.
        </p>
      </LegalSection>
      
      <LegalSection heading="9. Jogue com responsabilidade">
        <p>
          Jogos envolvendo recursos financeiros podem causar dependência e
          devem ser utilizados com responsabilidade.
        </p>
        
        <p>
          Nunca utilize recursos destinados a despesas essenciais, assuma
          compromissos financeiros em razão de jogos ou tente recuperar
          perdas mediante novas apostas.
        </p>
        
        <p>
          Caso a atividade deixe de ser recreativa, recomenda-se procurar
          auxílio especializado.
        </p>
        
        <p>
          Centro de Valorização da Vida (CVV):
          <strong> 188</strong>
        </p>
        
        <p>
          <a
            href="https://www.cvv.org.br"
            target="_blank"
            rel="noreferrer"
            className="underline underline-offset-4"
          >
            https://www.cvv.org.br
          </a>
        </p>
        
        <p>
          Jogadores Anônimos:
        </p>
        
        <p>
          <a
            href="https://jogadoresanonimos.com.br"
            target="_blank"
            rel="noreferrer"
            className="underline underline-offset-4"
          >
            https://jogadoresanonimos.com.br
          </a>
        </p>
      </LegalSection>
      
      <LegalSection heading="10. Natureza dos jogos">
        <p>
          Os jogos disponibilizados pela plataforma poderão possuir regras
          próprias e termos específicos.
        </p>
        
        <p>
          A participação em qualquer modalidade pressupõe aceitação das
          respectivas regras aplicáveis.
        </p>
        
        <p>
          A CTech poderá alterar, suspender ou descontinuar modalidades de
          jogos a qualquer momento, observadas as obrigações legais
          eventualmente aplicáveis.
        </p>
      </LegalSection>
      
      <LegalSection heading="11. Alterações">
        <p>
          Este documento poderá ser atualizado periodicamente.
        </p>
        
        <p>
          Alterações materiais dependerão de novo aceite antes da
          continuidade de utilização das funcionalidades relacionadas a
          jogos.
        </p>
      </LegalSection>
      
      <LegalSection heading="12. Contato">
        <p>
          A O CARVALHO TECH
        </p>
        
        <ul className="list-disc pl-5 space-y-2">
          <li>CNPJ: 62.787.449/0001-07</li>
          <li>DPO: Artur Oliveira Carvalho</li>
          <li>dpo@aoctech.app</li>
          <li>legal@aoctech.app</li>
          <li>(86) 9 8803-3430</li>
        </ul>
      </LegalSection>
    </LegalPage>
  )
}

