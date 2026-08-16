# Rollout controlado de custódia Asaas

**Data:** 2026-08-16  
**Status:** implementado no `ctech-wallet`; requer suporte correspondente no `ctech-pix-gateway` antes da ativação.

## Escopo

O Asaas é o provedor BaaS das subcontas de custódia e de cobranças por cartão.
O Inter continua sendo o provedor das cobranças PIX que caem diretamente na CTech.
Não há cobrança de taxa de saque CTech: um saque debita exatamente o valor solicitado.

## Controle de ativação

`ASAAS_CUSTODY_ENABLED=true` habilita a capacidade do serviço, os endpoints de
onboarding e os webhooks do Asaas. Não migra usuários automaticamente.

Cada item de carteira `real` possui o atributo interno, administrado diretamente
no DynamoDB, `custody_enabled: true`. Só uma carteira com esse atributo usa a
subconta Asaas para depósito e saque. Uma carteira sem o atributo continua no
Inter. O atributo não é exposto pela API e não há rota de autoativação.

O limite por depósito já é aplicado antes da cobrança: `max_deposit` é o teto
por carteira (admin-only); o teto absoluto `MaxInboundAmount` não pode ser
ultrapassado por override. Para reduzir risco de PLD/FTP, a allowlist deve ser
ativada junto com um `max_deposit` apropriado para cada usuário, começando com
valores baixos e revisão operacional.

## Crédito de depósito Asaas

O webhook `PAYMENT_RECEIVED` só acorda o processamento. Ele contém
`payment.id` e `payment.externalReference`; o serviço então:

1. encontra o depósito pela referência externa;
2. persiste o ID de pagamento de modo idempotente;
3. consulta o pagamento no Asaas com a credencial da subconta;
4. consulta o cliente indicado pelo pagamento;
5. compara o `cpfCnpj` retornado com o CPF KYC da wallet;
6. credita saldo e ledger em uma única operação idempotente somente se status,
   valor e CPF forem compatíveis.

Valor, status e CPF presentes apenas no webhook não são usados para crédito.

## Divergência de CPF e devolução

Se o CPF do cliente retornado pelo Asaas divergir do CPF KYC da carteira, a
Wallet persiste `refund_pending` sem creditar saldo e solicita o estorno integral
com `POST /v3/payments/{payment_id}/refund`, pela mesma subconta que recebeu o
PIX. Portanto este caminho devolve ao pagador via Asaas e nunca chama a API do
Inter. Nenhuma taxa CTech é descontada do valor devolvido.

Antes de cada repetição, a Wallet consulta o pagamento. O estado `REFUNDED`
encerra o estado local sem nova chamada de estorno. Essa observação é necessária
porque o Asaas suporta múltiplos estornos parciais; após falha de rede a saga
permanece em `refund_pending`/`refund_failed` e o reconciliador consulta antes
de decidir uma nova tentativa.

## Dependência de implantação

O contrato RPC compartilhado ganhou `AsaasQueryCustomer`, `AsaasRefundPayment`
e `customer_id` em `AsaasPaymentResult`. O `ctech-pix-gateway`, que executa as
chamadas externas para o Asaas, precisa implementar `GET /v3/customers/{id}` e
`POST /v3/payments/{id}/refund`, preenchendo `customer_id` da consulta de
pagamento e convertendo centavos para reais no estorno. Não habilite
`ASAAS_CUSTODY_ENABLED` em produção antes de publicar esse componente junto com
o `ctech-wallet`.

## Sequência operacional

1. Publicar `ctech-pix-gateway` e `ctech-wallet` compatíveis.
2. Configurar os segredos Asaas e `ASAAS_PARENT_WALLET_ID`; habilitar a flag
   global.
3. Criar/confirmar uma carteira real de teste com `custody_enabled=true` e um
   `max_deposit` baixo.
4. Executar onboarding, aguardar aprovação da subconta e validar depósito e
   saque reais de ponta a ponta.
5. Conferir conservação entre subconta e ledger antes de incluir cada novo
   usuário na allowlist.
