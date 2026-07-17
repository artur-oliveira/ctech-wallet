# ctech-wallet — Design

> ⚠️ **Parcialmente superado** por `docs/specs/2026-07-12-three-wallet-topology-design.md`.
>
> A carteira agora tem **três** saldos (`real`, `game`, `sandbox`), não dois. Dinheiro real chega aos jogos
> **somente** pela aresta `real → game`, e os créditos sandbox são comprados com o saldo `game` — nunca com o
> saldo real. Onde este documento disser "dois saldos" ou "compra de sandbox com saldo real", vale o documento
> novo. O restante (PIX, ledger, idempotência, locking, reconciliação) segue válido.

**Data:** 2026-07-10
**Status:** Aprovado
**Escopo:** repo novo `ctech-wallet` (api Go + ui Next.js + cdk). Consome KYC do ctech-account. Base para
assinaturas/billing e integração poker/dominó (fora de escopo deste documento).

## Objetivo

Serviço de carteira digital com dois saldos por usuário — **real** (PIX via Inter, saque/depósito) e **sandbox**
(dinheiro virtual, nunca convertível em real) — servindo de base para débito de assinaturas (dfe) e apostas
(poker/dominó). Ledger append-only, transacional (uma operação por wallet por vez), idempotente, sem saldo negativo.

## Decisões

| Decisão                       | Escolha                                                                                                                                                                          |
|-------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| Estrutura                     | Repo novo (`ctech-wallet`), mesmo padrão do `ctech-dfe` (api/ui/cdk). Valida JWT via JWKS do account.                                                                            |
| Modelo de dados               | `wallets` (saldo atômico, `GetItem`) + `ledger_entries` (append-only, auditoria) — não deriva saldo do ledger.                                                                   |
| Lock por wallet               | Valkey `SETNX wallet:{id}` com TTL curto (10s) + retry/backoff. Falha segura se processo morrer.                                                                                 |
| Integração PIX                | API Inter **Cobrança Imediata** (paga, taxa baixa repassada no saque) + webhook. Webhook nunca credita direto — sempre reconfirma via consulta do txid antes de gravar.          |
| CPF divergente no 1º depósito | Rejeita e estorna automaticamente (PIX devolução) — não credita.                                                                                                                 |
| Saque                         | Gate `kyc_level == verified` + step-up MFA. Sem chave PIX informada pelo cliente — o PIX é sempre enviado ao CPF do KYC. Taxa por-wallet (default 2%, mín. R$1, máx. R$10; piso absoluto R$1; admin-only). |
| Endereço no KYC               | Fora de escopo aqui — KYC atual (CPF+nome+nascimento) já libera depósito/saque; endereço fica para depois.                                                                       |
| Compra de sandbox com real    | Débito da wallet real (1 transação atômica cross-wallet), não é fluxo PIX novo.                                                                                                  |
| Escopos M2M                   | `internal:wallet:credit` / `internal:wallet:debit` (sandbox only, poker/dominó) — granularidade separada de depósito/saque real, que só a própria wallet executa.                |

## Fora de escopo

Billing/assinaturas, integração poker/dominó, painel admin, endereço completo no KYC, conversão sandbox→real (nunca
existe), suporte a outro PSP além do Inter.

---

## A. Modelo de dados (DynamoDB)

```
wallets         PK wallet_id (ULID)
                user_id, type ("real"|"sandbox"), balance (int, centavos), version, created_at
                fee_bps, fee_min, fee_max (opcionais, override de taxa por-wallet, admin-only via DynamoDB)

ledger_entries  PK wallet_id, SK ts#entry_id (ULID)
                type ("deposit"|"withdraw"|"fee"|"game_debit"|"game_credit"|"sandbox_purchase")
                amount (assinado, centavos), balance_after, idempotency_key (GSI única), ref, created_at

pix_deposits    PK txid
                wallet_id, amount_expected, status ("pending"|"confirmed"|"rejected_cpf_mismatch"|"expired")
                expires_at (TTL Dynamo, 5min; devolvido na resposta de POST /wallet/deposits como "expires_at")
```

GSI `user_id` em `wallets` → lista as 2 wallets do usuário. GSI `idempotency_key` em `ledger_entries` → rejeita
replay (webhook duplicado, retry de cliente).

### Domínio `internal/domain/wallet`

- `Repository`: `GetWallet`, `GetWalletsByUser`, `Credit(ctx, walletID, amount, entry)`, `Debit(ctx, walletID, amount,
  entry)` — ambos via `TransactWriteItems` (update condicional em `balance` + put condicional em `ledger_entries`
  por `idempotency_key`).
- `Service`: `InitiateDeposit`, `ConfirmDeposit` (webhook + reconsulta), `Withdraw`, `PurchaseSandbox`,
  `CreditSandbox`/`DebitSandbox` (uso M2M), `Statement`.

## B. Regras de negócio

- **Saldo nunca negativo**: `ConditionExpression: balance >= :amount` em todo débito. Falha → 409
  `insufficient-balance`.
- **Idempotência**: toda operação exige `idempotency_key` (client-supplied para depósito/saque via header
  `Idempotency-Key`; interno = `txid` ou `wallet_id#round_id` para jogos). Replay com mesma key → retorna resultado
  anterior, não duplica.
- **Lock ordenado cross-wallet**: operações que tocam 2 wallets do mesmo user (compra sandbox) tomam lock sempre na
  ordem `real` → `sandbox`, evita deadlock.
- **Sandbox nunca vira real**: sem rota de saque/conversão para wallet `sandbox`. Enforced no handler (rota de saque
  só aceita `type=real`).

## C. Fluxo de depósito (real)

1. `POST /wallet/deposits` (Bearer usuário, `Idempotency-Key`) — gate: `kyc_level != none`. Cria cobrança imediata na
   API Inter (valor + txid) → grava `pix_deposits` (status `pending`, TTL 15min) → retorna QR/copia-e-cola.
2. Webhook Inter → `POST /internal/pix/webhook`. Nunca credita a partir do payload do webhook isoladamente: sempre
   consulta a cobrança pelo `txid` na API Inter (confirma valor, status, CPF do pagador) antes de gravar qualquer coisa.
3. Consulta confirma pagamento:
    - **CPF pagador == CPF do KYC** (ou usuário já `verified`): `TransactWriteItems` credita `wallets.balance` +
      grava `ledger_entries` (idempotency_key = txid) → `pix_deposits.status = confirmed`. Se `kyc_level == basic`
      (1º depósito), chama `POST internal/kyc/confirm` (promove a `verified`).
    - **CPF pagador != CPF do KYC**: não credita. `pix_deposits.status = rejected_cpf_mismatch`. Dispara PIX devolução
      (API Inter, referenciando o e2eId do pagamento) — estorno automático ao pagador. Evento de auditoria
      `wallet.deposit_rejected_cpf_mismatch`.
4. Expiração (`pix_deposits` TTL): cobrança não paga em 5min é descartada; sem estado pendente eterno. A resposta de
   `POST /wallet/deposits` inclui `expires_at` (epoch da expiração) para o frontend mostrar contagem regressiva.

## D. Fluxo de saque (real)

1. `POST /wallet/withdrawals` (Bearer usuário + `RequireRecentMFA`, `Idempotency-Key`, body só `{amount}`). Gate:
   `kyc_level == verified`. **Sem `pix_key` no request** — o cliente nunca escolhe o destino; o PIX sai sempre para
   o CPF do registro de KYC do usuário (`kycclient.Get`).
2. `Valkey SETNX wallet:{id}` — lock exclusivo; falha → 409 `wallet-busy` (já existe operação em andamento).
3. Calcula taxa **por-wallet**: usa `fee_bps`/`fee_min`/`fee_max` do registro da wallet quando presentes (default
   2% / R$1 / R$10), `clamp(amount*bps/10000, min, max)`, nunca abaixo do piso absoluto de 100 centavos (R$1).
   Campos de taxa são admin-only (editados direto no DynamoDB, sem rota de API). `TransactWriteItems`: debita
   `amount + fee` (condição `balance >= amount + fee`) + 2 `ledger_entries` (`withdraw`, `fee`), mesma
   `idempotency_key` base.
4. Chama transferência PIX (`Transfer`) com o CPF do KYC como chave. Três desfechos:
    - **Sucesso** → `completed`. Notifica o usuário via WebSocket (`withdraw_completed`).
    - **Chave não registrada no banco** (Inter retorna 404 → `pix.ErrKeyNotFound`, sentinela
      `rpc.ErrKeyNotFoundSentinel` na borda pix-gateway↔api): nada a repetir — estorna **na hora** (mesmo helper
      `reverse()` usado pela reconciliação, idempotency key `reverse#<withdrawal_id>`), responde 422
      `pix-key-not-found`, e notifica o usuário via WebSocket (`withdraw_reversed` ou `withdraw_refund_failed` se o
      próprio estorno falhar — nesse caso também dispara o alarme operacional de sempre).
    - **Qualquer outro erro** (rede, Inter fora do ar): estado `processing`; o job de reconciliação confere o status
      na API Inter (chamada síncrona no `Transfer`, mas o desfecho pode ficar incerto em caso de falha de
      transporte) e conclui ou estorna o débito interno (nunca deixa em limbo sem job de resolução) — também
      notifica via WebSocket ao resolver.
5. Libera lock (sucesso, falha ou timeout do TTL).

## E. Sandbox

- **Crédito por app** (poker/dominó): `POST /internal/wallet/sandbox/credit` — Bearer client_credentials, scope
  `internal:wallet:credit`. Body `{user_id, amount, idempotency_key, reason}`.
- **Débito por app** (aposta): `POST /internal/wallet/sandbox/debit` — scope `internal:wallet:debit`. Mesma condição
  de saldo suficiente; app decide política de erro (mesa recusa jogada).
- **Compra com dinheiro real**: `POST /wallet/sandbox/purchase` (Bearer usuário). `TransactWriteItems` cruzando as 2
  wallets do mesmo user: debita `real` (condição saldo) + credita `sandbox`, mesma `idempotency_key`, locks tomados
  na ordem `real` → `sandbox`.

## F. Rotas

| Rota                                   | Auth                                                 | Ação                            |
|----------------------------------------|------------------------------------------------------|---------------------------------|
| `GET /wallet`                          | Bearer (user)                                        | saldo real + sandbox            |
| `POST /wallet/deposits`                | Bearer (user)                                        | inicia cobrança PIX             |
| `POST /internal/pix/webhook`           | segredo/mTLS do webhook Inter (não é JWT do account) | callback de pagamento           |
| `POST /wallet/withdrawals`             | Bearer (user) + `RequireRecentMFA`                   | saque real                      |
| `POST /wallet/sandbox/purchase`        | Bearer (user)                                        | compra sandbox com saldo real   |
| `GET /wallet/:type/ledger`             | Bearer (user)                                        | extrato paginado (`cursor`)     |
| `POST /internal/wallet/sandbox/credit` | client_credentials, scope `internal:wallet:credit`   | crédito sandbox (apps)          |
| `POST /internal/wallet/sandbox/debit`  | client_credentials, scope `internal:wallet:debit`    | débito sandbox (apps)           |
| `GET /v1.0/health`                     | pública                                              | liveness (sem dependências)     |
| `GET /v1.0/health-check`               | pública                                              | health detalhado (probe do ALB) |

### Health check

`GET /v1.0/health-check` segue o mesmo formato do `ctech-dfe` (draft-inadarei-api-health-check): `status`,
`version`, `releaseId` (= `APP_VERSION`, injetado pelo CI via `release.env`), `serviceId`, `description` e um mapa
`checks` com `uptime`, `dynamodb`, `cache`, `pix`, `jwks`, `cpu` e `memory` (cada um com `observedValue` /
`observedUnit` / `status`).

Agregação: qualquer `fail` → `503`; senão qualquer `warn` → `207`; senão `200`. O target group do ALB aceita
`200,207`, então uma instância degradada continua servindo.

- **`dynamodb` é o único check load-bearing** (`fail`): sem ele nenhuma operação de wallet é possível.
- **`cache` (Valkey), `pix` (Inter) e `jwks` (account) degradam para `warn`**: há fallback in-memory para
  cache/lock, o PIX só é necessário nos fluxos de depósito/saque, e o JWKS em cache sobrevive a uma queda breve do
  account.
- `pix` usa o token OAuth em cache (`PixClient.Ping`) — não movimenta dinheiro.

`GET /v1.0/health` não toca dependência nenhuma e devolve `status` + `releaseId` — útil para conferir qual release está
rodando na instância.

Erros RFC 7807 (mesmo padrão do account, `apierror` próprio no novo repo): `insufficient-balance` (409),
`wallet-busy` (409), `pix-key-not-found` (422, CPF do KYC sem chave PIX registrada no banco — estorna e responde),
`kyc-not-verified` (403), `idempotency-conflict` (409, mesma key com payload diferente). `withdraw-cpf-mismatch`
(403) é código legado de quando o cliente informava a própria chave PIX — não é mais alcançável (não há mais chave
vinda do cliente para divergir), mantido só por compatibilidade de schema.

## G. Escopos

Novo family `internal` (já existe no catálogo do account): `internal:wallet:credit`, `internal:wallet:debit`. Seed
via `cmd/seedscopes` do account (catálogo é global, cross-service). Client M2M de cada app (poker, dominó) recebe só
o subset necessário via `AllowedScopes`. Client M2M da própria wallet (chamando `internal:account:kyc`) já previsto no plano
do KYC.

## H. Testes

- **Unit**: cálculo de taxa (fronteiras min/max), lock ordenado cross-wallet, saldo nunca negativo, idempotência
  (replay retorna mesmo resultado, não duplica ledger).
- **Integração**: depósito completo (webhook mock → consulta mock → crédito), CPF divergente → rejeita + estorno
  chamado, saque completo (débito+taxa → transferência para o CPF do KYC), saque com chave não registrada no banco →
  422 `pix-key-not-found` + estorno imediato, saque concorrente na mesma wallet → segundo 409 `wallet-busy`, compra
  sandbox debita real e credita sandbox atomicamente, crédito/débito sandbox via client sem scope → 403.
- Mock `PixClient` (interface: `CreateCharge`, `QueryCharge`, `Transfer`, `Refund`) — fake em testes, real
  (mTLS, via pix-gateway) em produção.

## I. Cross-project

- **ctech-account**: nenhuma mudança de código — só operacional (seed do client M2M da wallet com scope
  `internal:account:kyc`; seed dos scopes `internal:wallet:*` no catálogo global via `cmd/seedscopes`).
- **ctech-dfe**: futuro consumidor (billing/assinaturas debitando via `internal:wallet:debit` — fora de escopo aqui).
- **poker/dominó (futuro)**: consomem `internal:wallet:sandbox/*`; 18+ e KYC já garantidos pelo claim `kyc_level` do
  account, wallet não reverifica.

## Riscos

- **Webhook forjado**: mitigado por nunca confiar no payload — toda confirmação reconsulta a API Inter pelo txid
  antes de gravar. Webhook é só um "acorda e confere", não fonte de verdade.
- **Falha na chamada de transferência PIX pós-débito interno**: dinheiro já saiu do saldo do usuário mas PIX pode não
  ter sido efetivado. Mitigado por estado `processing` + job de reconciliação obrigatório antes de marcar
  `completed`/estornar — não pode ficar sem resolução automática.
- **Estorno (devolução) falha** (CPF divergente ou saldo insuficiente na conta PJ): dinheiro fica na conta PJ sem
  dono. Precisa de alerta operacional (log/alarm) para reconciliação manual — não é um caminho "silencioso".
- **Enquadramento regulatório**: custódia de saldo de terceiro + taxa de saque pode se enquadrar em regulação do
  Banco Central (instituição de pagamento). Fora do escopo técnico deste documento — revisão jurídica recomendada
  antes de operar com volume relevante de dinheiro real.
