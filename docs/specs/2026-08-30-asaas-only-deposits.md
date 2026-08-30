# Depósitos exclusivamente via custódia Asaas — design

**Data:** 2026-08-30
**Status:** implementado
**Supersede:** `2026-08-16-asaas-custody-rollout.md` na parte de rollout gradual (a allowlist muda de
função) e o fallback Inter de `InitiateDeposit`.

## Problema

`POST /v1.0/wallet/deposits` abre uma cobrança PIX na conta **Inter da CTech** sempre que a carteira
não está em custódia Asaas (`services/wallet.go:744-749`). Como `ASAAS_CUSTODY_ENABLED` está
desligado em produção, **todo depósito hoje cai na conta da empresa**. Dinheiro de terceiro na conta
operacional da CTech é exatamente o que o modelo de custódia existe para evitar.

## Princípio

**Duas contas, dois propósitos, nunca cruzam:**

| Conta | Recebe | Motivo |
|---|---|---|
| **Inter (CTech)** | compras de produto de catálogo, `OpenCharge` de faturas | o usuário compra algo da CTech; o dinheiro é receita |
| **Asaas — subconta do usuário** | depósitos na carteira `real` | dinheiro do usuário, custodiado em conta no CPF dele |
| **Asaas — conta master CTech** | taxa de verificação de subconta | serviço prestado pela CTech, e é de onde o Asaas debita o próprio custo |

A regra do `CLAUDE.md` "Inter só para compras" ganha uma exceção nomeada: **taxas que o Asaas cobra da
CTech são cobradas na master Asaas**, porque é o saldo de onde o Asaas debita. Cobrar em Inter
deixaria o dinheiro na conta errada para pagar a conta certa.

---

## Achados na documentação do Asaas (verificados em 2026-08-30)

Dois deles são bloqueadores que o fluxo proposto não previa.

### 1. Existe re-query autoritativa de status — o webhook sai da quarentena

`api/internal/api/v1/baas.go` hoje **descarta** todo evento `ACCOUNT_STATUS_*` com um ALARM
("quarantined pending authoritative re-query"), porque não havia como confirmar o status fora do
webhook. Ou seja: **`ProcessAccountStatusWebhook` e `handleAccountApproved` nunca rodam hoje** — nenhuma
subconta é aprovada, nenhuma chave EVP é criada. Isso é o que trava o fluxo inteiro, não a chave EVP.

Não é um bloqueador de ordem: **ninguém deposita hoje**. O uso atual é carteira `sandbox` + compras de
produto/fichas, e nada disso passa por `InitiateDeposit`. Cortar o caminho Inter pode vir primeiro,
sozinho, sem esperar a aprovação de subconta funcionar.

O endpoint existe:

```http
GET /v3/myAccount/status          (credencial da própria subconta)
→ { id, commercialInfo, bankAccountInfo, documentation, general }
```

Cada campo ∈ `PENDING | APPROVED | REJECTED | AWAITING_APPROVAL`. **A conta só está aprovada quando
`general == APPROVED`.** Evento de webhook correspondente: `ACCOUNT_STATUS_GENERAL_APPROVAL_APPROVED`.

Isso preserva a Invariante #11 na forma que ela merece: o webhook acorda, a re-query decide.

### 2. Documentos pendentes vêm DEPOIS da criação, não antes

O seu fluxo tem "solicitar campos adicionais obrigatórios → enviar solicitação de criação". A ordem
real do BaaS Asaas é o inverso:

1. `POST /v3/accounts` — com os dados que já temos do KYC (nome, CPF, nascimento, endereço, telefone,
   e-mail, `incomeValue`);
2. **aguardar no mínimo 15 segundos** (exigência explícita da doc);
3. `GET /v3/myAccount/documents` — o Asaas responde quais documentos faltam;
4. cada documento pendente traz, ou não, um `onboardingUrl`:
   - **com `onboardingUrl`** → redirecionar o usuário para o link. Enviar por API é rejeitado.
   - **sem `onboardingUrl`** → `POST /v3/myAccount/documents/{id}` com o arquivo.

Ou seja, "campos adicionais" não são campos: são documentos, e o caminho normal para PF (documento de
identificação + selfie) é o `onboardingUrl`. Isso simplifica muito o frontend — não construímos tela de
upload, redirecionamos.

**Reaproveitar os documentos do `ctech-account`: só se e quando fizer falta.** O account já guarda
`id_front`, `id_back` e `selfie_with_document` em S3 (`kyc/{user_id}/{document_id}`,
`ctech-account/api/internal/domain/kyc/model.go:117`), mas o internal `GET /v1.0/internal/kyc/:user_id`
devolve só a identidade — não os arquivos. Construir a ponte exigiria um endpoint interno novo no
account (presigned GET, escopo dedicado, TTL curto), e **o Asaas rejeita envio por API de qualquer
documento que tenha `onboardingUrl`** — que é o caso esperado para PF. Então: rodar
`GET /v3/myAccount/documents` na primeira subconta real e só construir a ponte se aparecer documento
sem `onboardingUrl`. Enquanto não aparecer, é código para um caso que não existe.

### 3. Período de avaliação regulatória — restrição operacional

A partir da **primeira subconta criada em produção**: até 10 subcontas, até R$ 2.000,00 em cobranças
por subconta, até 60 dias corridos. Atingido qualquer um dos três, criação de subconta e emissão de
cobrança são bloqueadas até a homologação. Também exige exibir marca/links/textos de responsabilidade
do Asaas nas telas de contato com o usuário final.

Sem clientes ainda e com o primeiro cadastro sendo a conta PF do próprio operador, isto não afeta o
projeto — fica registrado como restrição a observar no rollout, não como requisito de código.

A allowlist `custody_enabled` **fica** mesmo assim: deixa de gatear o depósito e passa a gatear só o
onboarding, que é o recurso caro (R$12,90 e uma vaga de subconta por tentativa).

### 4. QR estático já é o que o código faz

`CreateDepositCharge` já chama `POST /v3/pix/qrCodes/static` com `addressKey` = EVP da subconta e
`externalReference` = txid, e a conciliação já é por `pixQrCodeId`/`externalReference`. Nada a mudar
aqui além do cap mensal.

Confirmado com você: os **100 recebimentos gratuitos/mês são por conta**, logo cada subconta tem sua
própria cota de 100, e a master tem a dela.

---

## Máquina de estados do onboarding

```
(nenhuma linha)
  │ KYC enhanced + custody_enabled
  ▼
fee_pending ──── PIX de R$12,90 pago na master ────► fee_paid
  │                                                    │ POST /custody/account
  │ (expira: TTL da cobrança, usuário refaz)           ▼
  │                                              onboarding  (POST /v3/accounts feito)
  │                                                    │ ≥15s, GET /v3/myAccount/documents
  │                                                    ▼
  │                                            pending_documents ──(onboardingUrl entregue ao usuário)
  │                                                    │ documentos enviados
  │                                                    ▼
  │                                             pending_approval
  │                                          ┌─────────┴──────────┐
  │                          general=APPROVED│                    │general=REJECTED
  │                                          ▼                    ▼
  │                                      approved            pending_documents
  │                              (cria EVP, cria wallet real)  (reenvio, taxa NÃO é recobrada)
```

Os estados `onboarding`, `pending_documents`, `pending_approval`, `approved`, `frozen`, `closing`,
`subaccount_closed`, `closed` já existem em `domain/wallet`. Entram **`fee_pending`** e **`fee_paid`**.

**Rejeição não é terminal.** Confirmado com você: a taxa é consumida na criação da subconta e o Asaas
não estorna. Portanto `REJECTED` volta para `pending_documents` para reenvio, **nunca** cobra a taxa de
novo e **nunca** gera estorno. `BaasClosed` fica reservado para encerramento de verdade.

Isso muda o `custodyBlockReason` do `2026-08-29-deposit-gate.md`: `pending_documents` deixa de ser
"nada a fazer além de esperar" e passa a ser acionável — vira um `blocked_by` próprio.

| Status | `blocked_by` | Ação no card |
|---|---|---|
| ausente | `custody_absent` | "Abrir conta de pagamento" |
| `fee_pending` | `custody_fee_pending` | "Pagar taxa de verificação" (mostra o QR) |
| `fee_paid`, `onboarding` | `custody_pending` | aguardar |
| `pending_documents` | `custody_documents` | "Enviar documentos" → `onboardingUrl` |
| `pending_approval` | `custody_pending` | aguardar |
| `frozen`/`closing`/`closed`/`subaccount_closed` | `custody_blocked` | suporte |

---

## Taxa de verificação

R$ 12,90 (`12_90` centavos = `1290`), **em SSM**, não em constante Go — muda sem deploy.

Reutiliza `ProductPurchase` (`services/product_purchase.go`) inteiro: mesma linha, mesma
idempotência, mesmo sweep, mesmo confirm-por-re-query. Muda só onde a cobrança é aberta:

```go
// wallet.ProductPurchaseKindCustodyFee — cobrada na conta master Asaas, não no
// Inter, porque é dela que o Asaas debita a taxa da subconta.
```

- QR estático na chave EVP da **master** (`ASAAS_MASTER_PIX_KEY`), `externalReference` = `"vfee#" + purchaseID`.
- Confirmação: webhook `PAYMENT_RECEIVED` cujo `account.id == ASAAS_MASTER_ACCOUNT_ID` → re-query
  `QueryPayment` com a chave da master → status `RECEIVED` e valor conferem → status vira `fee_paid`.
- **Sem checagem de CPF do pagador.** É uma compra, não um depósito; qualquer um pode pagar pelo
  usuário. É a mesma postura de `product_purchase.go`.
- Nada é creditado em carteira nenhuma. A `real` do usuário nem existe ainda.

O prefixo `vfee#` é o que separa os dois caminhos de `PAYMENT_RECEIVED` no dispatcher — hoje todo
`PAYMENT_RECEIVED` vai direto para `ConfirmAsaasDeposit`.

Uma cobrança de taxa consome 1 dos 100 recebimentos grátis mensais da master: 100 usuários novos/mês
antes de pagar R$1,99 por cadastro. Aceitável, e é o lado da receita.

---

## Depósitos

### Remoção do caminho Inter

`InitiateDeposit`: o `else` que chama `s.pix.CreateCharge` sai. A condição vira dura —

```go
acc, err := s.requireCustodyApproved(ctx, userID)   // sempre exigido
if err != nil { return nil, nil, err }              // 409 wallet-onboarding
```

Sai junto:

- `SetCustodyEnabled` / o ramo `!cfg.AsaasCustodyEnabled` de `app.go:182` — a capacidade passa a ser
  sempre ligada, a flag `ASAAS_CUSTODY_ENABLED` some;
- `CustodyEnabledForUser` **fica**, mas só no `initiateOnboarding` (achado #3);
- `queryDeposit`: o ramo `s.pix.QueryCharge` fica **apenas** para depósitos históricos com
  `Provider != ProviderAsaas`, e `Provider` passa a ser sempre preenchido em depósitos novos;
- `ConfirmDeposit` (webhook Inter) e o job de reconciliação Inter **ficam**, para drenar depósitos
  Inter já pagos ou pendentes. São caminho de leitura de histórico, não de abertura.
- `s.pix` (Inter) continua vivo: compras, `OpenCharge`, saques legados.

**Migração:** depósitos Inter `pending` no momento do deploy têm TTL de `depositTTLMinutes` e morrem
sozinhos. Nenhum backfill.

### Cap mensal de recebimentos

Contador por subconta, no padrão que já existe em `GameDepositCounters` (chave de janela + soma;
chave diferente = janela virou, soma é logicamente zero):

```go
// BaasAccount
ReceiptsMonthKey string `dynamodbav:"receipts_month_key,omitempty"` // "2026-08", fuso America/Sao_Paulo
ReceiptsCount    int64  `dynamodbav:"receipts_count,omitempty"`
```

- Incrementa em **`ConfirmDeposit`** (recebimento confirmado é o que o Asaas tarifa), atomicamente,
  na mesma transação que credita.
- Checa em **`InitiateDeposit`**, antes de abrir o QR → `429 deposit-receipts-exhausted` (novo
  problem type) com a data de reset.
- Teto padrão **95**, não 100, em SSM (`ASAAS_FREE_RECEIPTS_PER_MONTH`), override por carteira
  (`max_monthly_receipts`, admin-only como `max_deposit`).

```go
// ponytail: conta recebimentos CONFIRMADOS, não QRs abertos. Um QR aberto e não
// pago não é tarifado, então reservar slot na abertura seria contar errado — a
// margem de 5 abaixo dos 100 absorve os QRs em voo (TTL curto, um por vez por
// carteira via lock). Se algum dia o volume por usuário justificar, vira reserva
// com liberação no expiry.
```

---

## Saques

Nada a implementar. `AuthorizeTransfer` (`services/baas.go:533`) e a rota síncrona
`asaasTransferAuthorization` (`api/v1/asaas.go:88`) já existem, já comparam valor **e** destino contra
o `TransferIntent` gravado antes da submissão, e já recusam em qualquer divergência ou dado ausente.

Pendência é **operacional**: habilitar a autorização de transferências no painel Asaas apontando para
`POST /v1.0/internal/asaas/transfer-authorization`.

## Exposição dos webhooks — nada de API Gateway novo

O gateway com mTLS é exclusivo do Inter (`cdk/lib/pix-gateway-stack.ts`: função de webhook atrás de um
HTTP API com domínio custom mTLS-verificado, porque o Inter exige certificado cliente). **Os webhooks
do Asaas nunca passaram por ali.** Eles já estão na API principal:

```
POST https://wallet-api.aoctech.app/v1.0/internal/asaas/webhook
POST https://wallet-api.aoctech.app/v1.0/internal/asaas/transfer-authorization
```

gateados por `RequireAsaasWebhookToken` (`api/internal/middleware/asaas_webhook.go`), que compara em
tempo constante o header `asaas-access-token` com `/{service}/{env}/asaas/webhook-token` no SSM, e
**falha fechado com token vazio**. Nenhuma infra nova: só sair de trás do `if cfg.AsaasCustodyEnabled`
em `router.go:93`, que some junto com a flag.

O que vale endurecer, já que o segredo estático é a única barreira: restringir a origem por IP na
regra do ALB às faixas publicadas do Asaas. Confirmar as faixas com o suporte deles antes — se não
publicarem faixa estável, fica só o token, que é o que o Asaas oferece.

---

## Configuração (SSM Parameter Store)

| Parâmetro | Uso |
|---|---|
| `ASAAS_MASTER_ACCOUNT_ID` | dispatch do webhook: paga na master vs. na subconta |
| `ASAAS_MASTER_PIX_KEY` | chave EVP da master, base do QR estático da taxa |
| `ASAAS_VERIFICATION_FEE_CENTS` | `1290` |
| `ASAAS_FREE_RECEIPTS_PER_MONTH` | `95` |
| ~~`ASAAS_CUSTODY_ENABLED`~~ | **removido** |

`ASAAS_PARENT_WALLET_ID` e o segredo da API key da master continuam como estão.

## Contrato genérico

Sem camada nova. O que já existe é suficiente e o resto seria interface especulativa:

- `asaas.AsaasClient` é a fronteira do provider, com `fake.go` para teste;
- o domínio já é neutro (`BaasAccount`, `ProviderAsaas`, `TransferIntent`, `EVPPixKey`);
- as chamadas externas já saem por RPC via `ctech-pix-gateway`.

**Regra que vale a pena manter:** nenhum atributo DynamoDB novo, nenhuma rota e nenhum campo de DTO
público com `asaas` no nome — `custody`, `provider_*`, `baas`. Trocar de provider deve ser uma
implementação nova de `AsaasClient` (renomeável para `CustodyClient` numa passada mecânica) + um
mapeamento de status, não uma migração de schema.

## Impacto cross-project

| Componente | Mudança |
|---|---|
| `ctech-wallet/api` | tudo acima |
| `ctech-pix-gateway` | **novos:** `GET /v3/myAccount/status`, `GET /v3/myAccount/documents`. `POST /v3/myAccount/documents/{id}` só se aparecer documento sem `onboardingUrl`. Publicar antes da etapa 3. |
| `rpc-contract` | `AsaasQueryAccountStatus`, `AsaasListPendingDocuments` |
| `ctech-wallet/ui` | estados `custody_fee_pending` (QR da taxa) e `custody_documents` (link `onboardingUrl`); marca/links do Asaas nas telas de onboarding (exigência regulatória) |
| `ctech-account` | nenhuma. `kyc_level == "enhanced"` já é o `wallet.KYCVerified` (`domain/wallet/user.go:17`). Endpoint interno de documentos: só se a etapa 5 provar que faz falta |
| `ctech-wallet/cdk` | 4 parâmetros SSM novos, 1 removido. Nenhum API Gateway novo |

**Correção de documentação:** o `CLAUDE.md` da raiz diz `kyc_level` ∈ `""|basic|verified`. O valor
real é `enhanced` desde sempre no código; só o doc está velho.

## Onboarding não esconde saldo

`GET /v1.0/wallet` devolve `real` **sempre**, em qualquer estágio de onboarding.
`custody_status` é campo informativo, nunca supressor.

A regra anterior (plan §4.1) anulava `real`, `game` e `sandbox` até a subconta ser
aprovada, justificando com "uma carteira real não pode existir antes de uma conta
de custódia que a lastreie". Essa justificativa é sobre **não criar** a carteira, e
não se sustentava: `EnsureRealWallet` roda incondicionalmente antes do branch, então
a linha existe de todo jeito — esconder depois não protegia nada. O que ela fazia era
tirar `real` de uma resposta cujo contrato o exige (o frontend quebrava em
`real.wallet_id`) e prender a carteira `sandbox`, que é moeda de brincadeira sem
nenhuma relação com custódia e está em uso hoje, atrás de um passo que não é dela.

A Invariante #13 não é afetada: a conservação compara saldo com o da subconta, e um
usuário não onboardado não consegue depositar (`requireCustodyForDeposit`), logo o
saldo de partida é zero. O portão fica em `InitiateDeposit` e é relatado por
`DepositReadiness`. Regressão: `TestOnboardingNeverHidesBalances`.

## Invariantes afetadas

- **#11** — reforçada em dois lugares: o webhook `ACCOUNT_STATUS_*` deixa de ser descartado e passa a
  acordar `GET /v3/myAccount/status`; a taxa só confirma após `QueryPayment` na master.
- **#12** — a taxa de verificação é explicitamente **não estornável** e isso é uma decisão de negócio
  documentada, não dinheiro em limbo: o custo já foi consumido no Asaas e a rejeição não é terminal
  (o usuário reenvia documento sem pagar de novo). Precisa estar no texto que o usuário aceita antes
  de pagar.
- **Nova (#14)** — *nenhum depósito em carteira de usuário abre cobrança na conta Inter da CTech.*
  Teste de regressão executável: `TestDepositNeverChargesInter` — `InitiateDeposit` sem subconta
  aprovada retorna `wallet-onboarding` e o `pix.PixClient` fake não recebe nenhuma chamada.

## Plano de implementação

### Onde ficou

| Peça | Arquivo |
|---|---|
| Gate de depósito (sem fallback) | `api/internal/services/wallet.go` `requireCustodyForDeposit`, `InitiateDeposit` |
| Cap mensal de recebimentos | `wallet.go` `requireReceiptAllowance`; contador em `repositories/baas.go` `IncrementReceiptCount` |
| Taxa de verificação | `api/internal/services/custody_fee.go` |
| Re-query autoritativa de status | `services/baas.go` `ProcessAccountStatusWebhook`, `syncPendingStatus` |
| Documentos pendentes | `asaas.ListPendingDocuments` → `pix-gateway` `GET /v3/myAccount/documents` |
| Rotas | `api/internal/api/v1/baas.go`, `router.go` |
| Frontend | `ui/src/components/wallet/custody-fee-dialog.tsx`, `deposit-gate.tsx` |

| # | Etapa | Verificação |
|---|---|---|
| 1 | **Remover fallback Inter** de `InitiateDeposit`, `ASAAS_CUSTODY_ENABLED` e o `if` do `router.go:93` | `TestDepositNeverChargesInter` |
| 2 | `rpc-contract` + `pix-gateway`: `GET /v3/myAccount/status` | testes do gateway; publicar antes da etapa 3 |
| 3 | Tirar `ACCOUNT_STATUS_*` da quarentena: webhook → re-query → `general` decide | teste: webhook com `general=PENDING` não aprova |
| 4 | Estados `fee_pending`/`fee_paid` + `ProductPurchaseKindCustodyFee` na master | integração: paga taxa → `fee_paid`; replay não recobra |
| 5 | `pending_documents` + `onboardingUrl` (`GET /v3/myAccount/documents`) | integração: doc com `onboardingUrl` nunca sobe por API |
| 6 | Cap mensal de recebimentos | integração: 95º depósito ok, 96º → 429; virada de mês zera |
| 7 | UI: dois estados novos no card + marca/links do Asaas | `npx eslint src --ext .ts,.tsx` zero erros/avisos |
| 8 | SSM/CDK; onboarding real da conta PF do operador ponta a ponta | depósito e saque reais; conferir conservação subconta ↔ ledger |

**Etapa 1 vai primeiro e sozinha.** Como ninguém usa depósito hoje, ela não precisa esperar a
aprovação de subconta funcionar: fecha o furo do dinheiro na conta Inter num diff pequeno e
independente, e deixa o resto do onboarding sendo construído sem pressa. As etapas 4 e 6 dependem de 3
(sem `approved` não há subconta para cobrar nem para receber).

Envio de documento por API (`POST /v3/myAccount/documents/{id}`) + ponte de documentos com o
`ctech-account` ficam **fora do plano** até a etapa 5 provar que algum documento vem sem
`onboardingUrl`.
