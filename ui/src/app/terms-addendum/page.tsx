import type {Metadata} from 'next'
import {LegalPage, LegalSection} from '@/components/legal-page'

export const metadata: Metadata = {
  title: 'Termos Adicionais — CTech Wallet',
  description:
    'Termos adicionais aplicáveis à utilização da CTech Wallet.',
}

const ADDENDUM_VERSION = '2.0'
const UPDATED_AT = '12 de julho de 2026'

export default function TermsAddendumPage() {
  return (
    <LegalPage
      title="Termos Adicionais — CTech Wallet"
      updatedAt={UPDATED_AT}
    >
      <p className="text-xs text-gray-400">
        Versão {ADDENDUM_VERSION}
      </p>
      
      <p>
        Este documento complementa os{' '}
        <a
          href="https://accounts.aoctech.app/terms"
          target="_blank"
          rel="noreferrer"
          className="underline underline-offset-4"
        >
          Termos de Uso
        </a>{' '}
        e a{' '}
        <a
          href="https://accounts.aoctech.app/privacy"
          target="_blank"
          rel="noreferrer"
          className="underline underline-offset-4"
        >
          Política de Privacidade
        </a>{' '}
        da plataforma CTech.
      </p>
      
      <LegalSection heading="1. Sobre a CTech Wallet">
        <p>
          A CTech Wallet é uma funcionalidade da plataforma CTech destinada
          ao gerenciamento de créditos e movimentações relacionadas aos
          serviços disponibilizados pela CTech.
        </p>
        
        <p>
          A Wallet não constitui conta bancária, conta de pagamento,
          investimento financeiro, depósito bancário ou produto de
          investimento regulado.
        </p>
        
        <p>
          Os valores mantidos na Wallet representam créditos perante a
          plataforma CTech para utilização nos serviços disponibilizados.
        </p>
        
        <p>
          Os recursos mantidos na Wallet não possuem cobertura do Fundo
          Garantidor de Créditos (FGC).
        </p>
      </LegalSection>
      
      <LegalSection heading="2. Elegibilidade">
        <p>
          Para utilização de funcionalidades envolvendo dinheiro real é
          necessário:
        </p>
        
        <ul className="list-disc pl-5 space-y-2">
          <li>possuir 18 (dezoito) anos ou mais;</li>
          <li>possuir conta CTech ativa;</li>
          <li>concluir o processo de verificação de identidade (KYC).</li>
        </ul>
        
        <p>
          A CTech poderá solicitar documentos adicionais a qualquer momento,
          inclusive para atualização cadastral ou prevenção à fraude.
        </p>
      </LegalSection>
      
      <LegalSection heading="3. Estrutura das carteiras">
        <p>
          A Wallet poderá possuir saldos segregados logicamente, incluindo:
        </p>
        
        <ul className="list-disc pl-5 space-y-2">
          <li>
            <strong>Saldo Real:</strong> destinado a serviços e
            movimentações financeiras.
          </li>
          
          <li>
            <strong>Carteira de Jogo:</strong> destinada exclusivamente às
            atividades de jogos integrados.
          </li>
          
          <li>
            <strong>Créditos Sandbox:</strong> moeda virtual sem valor
            econômico.
          </li>
        </ul>
      </LegalSection>
      
      <LegalSection heading="4. Depósitos">
        <p>
          Os depósitos são realizados exclusivamente por meio do sistema
          PIX, utilizando instituições financeiras parceiras.
        </p>
        
        <p>
          O crédito somente ocorrerá após confirmação efetiva da liquidação
          da operação pela instituição financeira responsável.
        </p>
        
        <p>
          A CTech poderá recusar, cancelar ou estornar depósitos quando:
        </p>
        
        <ul className="list-disc pl-5 space-y-2">
          <li>o CPF do pagador for divergente do CPF verificado;</li>
          <li>existirem indícios de fraude;</li>
          <li>houver determinação legal ou regulatória;</li>
          <li>forem identificadas movimentações incompatíveis.</li>
        </ul>
      </LegalSection>
      
      <LegalSection heading="5. Saques">
        <p>
          Os saques poderão ser realizados exclusivamente para chaves PIX
          pertencentes ao mesmo CPF verificado na conta.
        </p>
        
        <p>
          As solicitações de saque são processadas de forma instantânea
          sempre que possível, dependendo da disponibilidade:
        </p>
        
        <ul className="list-disc pl-5 space-y-2">
          <li>da instituição financeira parceira;</li>
          <li>do Sistema de Pagamentos Instantâneos (SPI);</li>
          <li>da infraestrutura do Banco Central do Brasil.</li>
        </ul>
        
        <p>
          Eventuais indisponibilidades poderão ocasionar atrasos no
          processamento.
        </p>
      </LegalSection>
      
      <LegalSection heading="6. Taxas">
        <p>
          A utilização de determinadas funcionalidades poderá estar sujeita
          à cobrança de taxas operacionais.
        </p>
        
        <p>
          Todas as taxas aplicáveis serão previamente informadas ao usuário
          antes da confirmação da operação.
        </p>
      </LegalSection>
      
      <LegalSection heading="7. Prevenção à fraude e AML">
        <p>
          A CTech poderá adotar medidas de prevenção à fraude, lavagem de
          dinheiro, financiamento ao terrorismo e utilização indevida da
          plataforma.
        </p>
        
        <p>
          Para tanto, poderão ser adotadas medidas como:
        </p>
        
        <ul className="list-disc pl-5 space-y-2">
          <li>imposição de limites de movimentação;</li>
          <li>solicitação de documentação adicional;</li>
          <li>revalidação cadastral;</li>
          <li>bloqueio preventivo de operações;</li>
          <li>suspensão temporária da conta;</li>
          <li>retenção temporária de valores para análise.</li>
        </ul>
        
        <p>
          Caso sejam identificados indícios razoáveis de fraude ou atividade
          ilícita, a CTech poderá comunicar autoridades competentes e adotar
          as medidas cabíveis.
        </p>
      </LegalSection>
      
      <LegalSection heading="8. Encerramento da conta">
        <p>
          O encerramento da Wallet somente poderá ocorrer após a liquidação
          integral dos saldos existentes.
        </p>
        
        <p>
          Enquanto houver saldo disponível ou operações pendentes, a conta
          poderá permanecer ativa exclusivamente para fins de regularização.
        </p>
      </LegalSection>
      
      <LegalSection heading="9. Créditos Sandbox">
        <p>
          Os créditos sandbox possuem natureza exclusivamente virtual.
        </p>
        
        <p className="font-medium">
          Créditos sandbox:
        </p>
        
        <ul className="list-disc pl-5 space-y-2">
          <li>não representam dinheiro eletrônico;</li>
          <li>não possuem valor monetário;</li>
          <li>não geram direitos patrimoniais;</li>
          <li>não podem ser sacados;</li>
          <li>não podem ser convertidos em dinheiro;</li>
          <li>não são reembolsáveis.</li>
        </ul>
        
        <p>
          A aquisição de créditos sandbox é definitiva e irretratável.
        </p>
      </LegalSection>
      
      <LegalSection heading="10. Disponibilidade do serviço">
        <p>
          A Wallet depende de serviços prestados por terceiros, incluindo
          instituições financeiras e infraestrutura PIX.
        </p>
        
        <p>
          A CTech não garante disponibilidade ininterrupta desses serviços e
          não se responsabiliza por falhas, indisponibilidades ou atrasos
          causados por terceiros.
        </p>
      </LegalSection>
      
      <LegalSection heading="11. Limitação de responsabilidade">
        <p>
          Na máxima extensão permitida pela legislação aplicável, a CTech
          não será responsável por:
        </p>
        
        <ul className="list-disc pl-5 space-y-2">
          <li>falhas da infraestrutura PIX;</li>
          <li>indisponibilidade de instituições financeiras;</li>
          <li>danos indiretos;</li>
          <li>lucros cessantes;</li>
          <li>eventos de força maior.</li>
        </ul>
        
        <p>
          Nada neste documento limita direitos indisponíveis previstos na
          legislação brasileira.
        </p>
      </LegalSection>
      
      <LegalSection heading="12. Alterações">
        <p>
          Este documento poderá ser alterado periodicamente.
        </p>
        
        <p>
          Alterações materiais dependerão de novo aceite do usuário antes da
          continuidade da utilização da Wallet.
        </p>
      </LegalSection>
      
      <LegalSection heading="13. Contato">
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

