# Restauração da CTech Wallet (rail Inter, limites, descrições, extrato)

**Data:** 2026-10-08
**Status:** aprovado para planejamento; planos em `docs/plans/2026-10-08-*.md`
**Substitui parcialmente:** `2026-10-07-asaas-removal.md` (seção "O que um provedor futuro precisa reintroduzir") e as Invariantes #12/#13.

## Contexto e objetivo

A Asaas reprovou o cadastro BaaS da CTech. O produto volta a se chamar **CTech Wallet** e o saldo `real`
volta a usar o PIX da conta Inter da CTech. Uso inicial: poucos amigos do dono, todos com KYC aprovado
manualmente por ele. O dono assume o risco jurídico da custódia.

Sucesso significa:

- depósito e saque PIX funcionando com os mesmos safeguards do rail pré-BaaS;
- limites diários por carteira, rígidos por padrão;
- toda transação M2M com descrição legível;
- extrato paginado com saldo antes/depois e aba de compras;
- falhas das Lambdas Inter chegando por SNS ao e-mail do dono.

## Sub-projetos (ordem de execução)

1. Rebrand e rail Inter com limites
2. `description` obrigatória (poker e billing primeiro)
3. API de extrato
4. Frontend
5. Alertas SNS nas Lambdas Inter

Cada sub-projeto vira um plano próprio (ordem sugerida: 1, 3, 5, 2, 4):

| # | Plano |
|---|-------|
| 1 | `docs/plans/2026-10-08-wallet-inter-rail-and-limits.md` |
| 2 | `docs/plans/2026-10-08-mandatory-transaction-description.md` (wallet, poker, billing; flag `REQUIRE_DESCRIPTION`) |
| 3 | `docs/plans/2026-10-08-statement-api-balance-before.md` |
| 4 | `docs/plans/2026-10-08-wallet-ui-statements-and-copy.md` (depende de 1 e 3 no dashboard e nos locales) |
| 5 | `docs/plans/2026-10-08-inter-lambda-sns-alerts.md` |

Itens 2, 3 e 5 são independentes entre si.

---

## 1. Rebrand e rail Inter

### Restauração

Restaurar do git (commit `e106390^`, antes da integração Asaas) em vez de reescrever:

- `POST /v1.0/wallet/deposits`: cobrança Inter, QR, webhook, re-consulta por `txid`, match de CPF mascarado
  (`maskedCPFMatches`), estorno em divergência. Gate de KYC e de faixa de depósito **antes** de `CreateCharge`.
- `POST /v1.0/wallet/withdrawals`: KYC `enhanced`, CPF da chave destino == CPF do KYC, `RequireRecentMFA`
  (5 min, `max_age=0` no `ctech-account`), estado `processing` resolvido pelo reconcile, estorno imediato em
  `ErrKeyNotFound`.
- Scopes `wallet:deposits:write` e `wallet:withdrawals:write` (já declarados, sem rota).

Ring-fence, holds, sandbox e jogo responsável não mudam. `TestSandboxPurchaseNeverDebitsRealWallet` continua verde.

### Match de CPF

O Inter mascara o CPF do pagador no webhook (ex.: `***137303**`); `maskedCPFMatches` compara só os dígitos
revelados. No saque a chave destino traz o CPF completo.

**Sweep já falha fechado (verificado no código atual):** `ConfirmDeposit` coloca em quarentena (ALARM + erro) um
depósito pago sem CPF de pagador registrado; nunca o credita. Nada a mudar aqui, apenas não regredir isso.

### Limites por carteira

Todos em centavos inteiros, editáveis só direto no DynamoDB (como `min_deposit`/`max_deposit`):

| Campo                  | Padrão | Significado                                          |
|------------------------|--------|------------------------------------------------------|
| `daily_deposit_cap`    | 100000 | Soma bruta de depósitos por dia (calendário BRT)     |
| `daily_withdraw_count` | 1      | Saques por dia                                       |
| `daily_withdraw_cap`   | 100000 | Valor máximo de saque por dia                        |

Aplicação: item contador por carteira/dia escrito com condição na **mesma** `TransactWriteItems` do movimento
de dinheiro (nunca read-then-write, Invariante #1). O teto de depósito é checado antes de abrir cobrança, então
um valor rejeitado nunca cria cobrança no Inter. Erros RFC 7807: `daily-deposit-limit`, `daily-withdraw-limit`
(409). Constantes nomeadas; sem números mágicos.

### Invariantes

- **#12 reescrita:** dinheiro de usuário cai na conta Inter da CTech sob aceite explícito e documentado do
  risco de custódia, para um grupo fechado com KYC aprovado pelo dono; todo depósito é atribuível a um usuário
  por match de CPF.
- **#13** continua aposentada.
- Atualizar `CLAUDE.md` (raiz e `api/`), `ENDPOINTS.md`, `OPERATIONS.md` e a nota de substituição no spec Asaas.

### Rebrand

Reverter strings voltadas ao cliente de "CTech Ledger" para "CTech Wallet" (UI, locales, docs, e-mails se
houver) e remover resquícios do selo Asaas.

### Testes

Integração (DynamoDB-local): teto diário de depósito sob concorrência; limite de saque; replay idempotente;
saque `processing` resolvido pelo reconcile; depósito pago sem CPF de pagador segue em quarentena; `wallet-busy`.

---

## 2. `description` obrigatória

Hoje é opcional (spec 2026-08-29). Passa a `validate:"required,min=3,max=255"` em todas as rotas M2M
(`sandbox/credit`, `sandbox/debit`, `real/debit`, `sandbox-purchase`, `product-purchase`, `charge` e game
cashout; o hold não gera lançamento no ledger e fica de fora). Ausente ou em branco => 400. Continua **fora** do hash de idempotência e é só metadado de exibição.

**Ordem de rollout (mudança quebrável):**

1. `ctech-poker` (`buyin`, `cosmeticpurchase`, `reactionpurchase`, `reconcile`, `tablecleanup`) e `ctech-billing`
   passam a enviar descrição real (ex.: `Mesa #<id>`, `Prêmio mesa #<id>`, `Assinatura <plano> <mês>`).
2. Deploy desses serviços.
3. Wallet liga a exigência atrás de `REQUIRE_DESCRIPTION` (padrão **desligado**); ligado após o passo 2.

**Repos externos:** antes de editar qualquer outro repo, rodar `git checkout main && git pull origin main`,
depois criar branch própria e abrir PR separado por repo.

Rótulos genéricos antigos ("Prêmio", "Jogada") permanecem como fallback de linhas históricas sem descrição.

---

## 3. API de extrato

- `balance_after` já existe no ledger. A resposta passa a expor `balance_before = balance_after - amount`,
  calculado no servidor, sem migração.
- `GET /wallet/:type/ledger`: `limit` com teto, `next_cursor` opaco, teste de paginação entre páginas.
- Histórico de compras: endpoints existentes (`/wallet/sandbox/purchases`, `/wallet/product-purchases`) com a
  mesma paginação por cursor. Não são fundidos no servidor (exigiria novo índice).

---

## 4. Frontend

- Scroll infinito (`IntersectionObserver` + `useInfiniteQuery`, padrão de `ctech-poker` `hands/page.tsx`) com
  estados de carregando, vazio e erro. Antes, checar `ctech-ui` por um componente compartilhado; se não houver,
  propor adicioná-lo lá.
- Cada linha: descrição como linha principal, valor com sinal, "saldo antes → depois".
- Abas: Real, Game, Sandbox e **Compras**. Dentro de Compras, dois filtros (Créditos sandbox, Produtos
  digitais), cada um com seu endpoint.
- Remover todo "—" (hoje 58 ocorrências) em `ui/src`: usar bullet para separar e `;` para pausar. Reduzir
  texto em favor de elementos autoexplicativos. Passe de copy em `locales/*.json` e componentes, sem redesenhar
  o layout.
- Gate: `npx eslint src --ext .ts,.tsx` com zero erros e zero warnings.

---

## 5. Alertas SNS nas Lambdas Inter

Copiar o padrão `alerts` de `ctech-billing` (`alerts.New(ctx, region, topicARN, service, env)`); se ainda não
estiver em `ctech-go-common`, propor movê-lo para lá e consumir de ambos. As Lambdas `webhook` e `outbound`
publicam no tópico compartilhado em qualquer falha: carga de config, carga de segredo, falha de chamada à
wallet, erro do Inter, payload malformado.

CDK: `sns:Publish` na role e `ALERTS_TOPIC_ARN` no ambiente. O alerta leva `txid`/request id; **nunca** CPF,
nome do pagador ou segredos. Falha ao publicar o alerta é logada e não derruba a resposta da Lambda.

---

## Impacto entre projetos

`api`, `ui`, `cdk`, `pix-gateway`, `rpc-contract` (se o contrato de saque mudar), `ctech-account`
(`max_age`/step-up já documentado), `ctech-poker`, `ctech-billing`, `ctech-go-common`, `ctech-ui`.

## Fora de escopo

Qualquer novo provedor de custódia; limites editáveis via API; unificação server-side de compras; remoção das
tabelas Asaas órfãs (PR separado, fase 2 do spec de remoção).
