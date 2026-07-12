# Aditivo aos Termos de Uso — CTech Wallet

**Status:** rascunho — a ser publicado em `wallet.aoctech.app/terms` (ou rota equivalente) quando o serviço existir.
Complementa, não substitui, os [Termos de Uso](https://accounts.aoctech.app/terms) e a
[Política de Privacidade](https://accounts.aoctech.app/privacy) do CTech Account. Em caso de conflito, este
aditivo prevalece no que for específico da carteira digital.

**Versão:** 1.0 (rascunho) — **Entidade:** A O CARVALHO TECH, CNPJ 62.787.449/0001-07, Rua Atleta Daniel Aragão
Matos, 6201, Vale Quem Tem, Teresina/PI — **Contato/DPO:** dpo@aoctech.app

Aceite explícito obrigatório no primeiro acesso à wallet (checkbox versionado + timestamp, mesmo padrão do
account: `wallet_tos_version` / `wallet_tos_accepted_at`), além do aceite do termo mestre já feito no cadastro
do account.

## 1. Natureza do serviço

A CTech Wallet é uma carteira digital que mantém dois saldos por usuário: **saldo real** (depósito/saque via
PIX) e **saldo sandbox** (moeda virtual, sem valor monetário, não resgatável, não conversível em dinheiro real
sob nenhuma circunstância).

## 2. Requisitos de acesso

- Saldo real: verificação de identidade (`kyc_level == verified`) e 18 anos ou mais, conforme regras do CTech
  Account.
- Saque: chave PIX de destino deve pertencer ao mesmo CPF verificado na conta. Divergência de CPF é rejeitada.

## 3. Depósitos

Depósitos são recebidos via PIX (Cobrança Imediata, API do banco parceiro). O valor só é creditado após
confirmação de pagamento pela consulta ao provedor — nunca com base isolada em notificação de webhook. Depósito
cujo CPF do pagador diverge do CPF verificado na conta é rejeitado e estornado automaticamente ao pagador.

## 4. Saques e taxa

Saques têm taxa de **2% sobre o valor**, com mínimo de **R$ 1,00** e máximo de **R$ 10,00** por operação — cobre o
custo de transferência PIX do provedor. O valor líquido (descontada a taxa) é transferido à chave PIX informada.
Uma única operação (depósito ou saque) pode estar em andamento por vez por carteira.

## 5. Saldo sandbox

O saldo sandbox pode ser adquirido com saldo real (débito da wallet real, sem trocas ou reembolsos em dinheiro)
ou concedido por aplicações integradas (ex. poker, dominó) como bônus. **Não é resgatável, não tem valor
monetário e não pode ser convertido em saldo real ou sacado sob nenhuma hipótese.**

## 6. Isenção de responsabilidade

A CTech Wallet não é uma instituição financeira licenciada pelo Banco Central do Brasil. O serviço atua como
intermediário técnico de custódia e movimentação de valores via PIX. A CTech não garante disponibilidade
ininterrupta do provedor de pagamentos e não se responsabiliza por atrasos, falhas ou indisponibilidades da
infraestrutura PIX de terceiros.

## 7. Uso em jogos (poker/dominó)

O saldo sandbox pode ser utilizado para participar de partidas em aplicações integradas de habilidade (poker,
dominó). A CTech Wallet não processa apostas com saldo real nesta versão inicial.

## 8. Alterações

Este aditivo pode ser atualizado; alterações materiais exigem novo aceite explícito antes de continuar usando a
wallet.

---

**Nota de implementação:** este documento é um placeholder de conteúdo — quando `ui/` da wallet existir, publicar
em rota própria e implementar o gate de aceite (mesmo padrão do `/accept-terms` do ctech-account: token único,
versão + timestamp persistidos, auditoria). Revisão jurídica recomendada antes de operar com dinheiro real de
terceiros (ver riscos regulatórios na spec de design, seção "Riscos").
