# CTech Wallet

Serviço de carteira digital do ecossistema `aoctech.app`. Mantém **três saldos por usuário** — **real**
(depósito/saque via PIX, banco parceiro Inter), **game** (dinheiro real cercado só para jogos, com limites de
jogo responsável) e **sandbox** (moeda virtual, sem valor monetário, nunca convertível em real). Dinheiro real
entra no cercado de jogo **somente** via `real → game`. Serve de base para cobrança de assinaturas (futuro
`ctech-dfe`/billing) e para apostas de habilidade em poker/dominó. A superfície de jogo (`game`/`sandbox`) fica
dormente enquanto `GAMBLING_ENABLED=false` (padrão), até a revisão legal do aditivo de jogo.

Consome autenticação (OAuth 2.0 / OIDC, JWT via JWKS) e KYC do [`ctech-account`](../ctech-account). Ledger
append-only, transacional, idempotente, sem saldo negativo.

## Documentação jurídica vigente

Os Termos da Wallet publicados pela Central Jurídica do CTech estão na versão
**2.2**; o termo separado de jogo responsável permanece na versão **2.1**. As
constantes de aceite em `api/internal/domain/wallet/user.go` devem acompanhar
essas versões. A fonte pública de verdade é
`https://accounts.aoctech.app/products/wallet`.

## Registro de scopes OAuth

A Wallet é dona do manifesto versionado
`api/internal/oauthresource/scope-manifest.json`, atualmente com 13 permissões
públicas `wallet:*` e 10 permissões M2M `internal:wallet:*`. O teste de contrato
compara o manifesto com as constantes realmente usadas pelo middleware. O deploy
o publica no CTech Account depois do CDK e antes da API usando um client
confidencial vinculado somente ao Resource Server `wallet`; o papel OIDC lê
apenas os três parâmetros necessários.

`GET /.well-known/oauth-protected-resource` implementa RFC 9728 e anuncia apenas
os 13 scopes públicos. Tokens delegados que carregam qualquer `wallet:*` são
limitados ao scope exato de cada rota; os scopes internos nunca são anunciados.
A UI solicita os 13 scopes junto de `openid profile kyc`. Durante a migração,
tokens first-party já emitidos sem `wallet:*` mantêm o acesso atual. Publicar
o manifesto também acrescenta os scopes públicos ao `allowed_scopes` do OAuth
Client first-party `wallet`, sem alterar redirects, audience ou outros clients
(consulte `OPERATIONS.md`).

## Documentação

| Documento                                                                              | Descrição                                              |
|----------------------------------------------------------------------------------------|--------------------------------------------------------|
| [`docs/specs/2026-07-10-wallet-design.md`](docs/specs/2026-07-10-wallet-design.md)     | Design aprovado — ledger, PIX, sandbox, escopos OAuth  |
| [`docs/legal/wallet-terms-addendum.md`](docs/legal/wallet-terms-addendum.md)           | Aditivo aos Termos de Uso (rascunho)                   |
| [`CLAUDE.md`](CLAUDE.md)                                                                | Instruções para Claude Code                            |
| [`AGENTS.md`](AGENTS.md)                                                                | Contexto para agentes de IA (idêntico ao `CLAUDE.md`)  |

## Subprojetos

```
api/          # Backend REST — Go (Fiber v3), DynamoDB, Valkey, Reconcile, PIX
ui/           # Frontend — Next.js 16 + TypeScript + ShadCN
cdk/          # Infraestrutura AWS — CDK TypeScript
pix-gateway/  # Provedor e mock de gateway PIX
rpc-contract/ # Contrato RPC e DTOs M2M
```

## Observabilidade de erros

A API usa `api-commons/observability` e `api-commons/observability/fiber`. Toda resposta RFC 7807 é registrada uma
vez na borda (`WARN` para 4xx, `ERROR` para 5xx), com `request_id`, método, path, tipo do problema e causa interna
quando disponível. `X-Request-ID` é preservado ou gerado, devolvido e exposto por CORS. Erros best-effort continuam
logados no ponto de consumo. Tokens, payloads financeiros, CPF, chaves PIX e demais dados sensíveis não entram nos
logs. A integração não habilita OpenTelemetry nem métricas customizadas.

## Segurança (sistema financeiro)

Este serviço custodia dinheiro real de terceiros. Invariantes não-negociáveis:

- **Saldo nunca negativo** — `ConditionExpression: balance >= :amount` em todo débito.
- **Ledger append-only** — saldo mora em `wallets` (atômico); `ledger_entries` é auditoria imutável.
- **Idempotência obrigatória** — toda operação exige `Idempotency-Key`; replay retorna o resultado anterior.
- **Uma operação por wallet por vez** — lock via Valkey `SETNX` com TTL curto.
- **Webhook nunca é fonte de verdade** — pagamento só credita após reconsulta ao provedor pelo `txid`.
- **Saque com gate** — `kyc_level == verified` + step-up MFA + CPF da chave PIX destino == CPF do KYC.
- **Sem taxa de saque** — o saque debita exatamente o `amount` e grava um único lançamento `withdraw`
  (`docs/specs/2026-08-16-withdrawal-fee-removal.md`). Tarifa da Asaas é custo de provedor (`transfer_fee`),
  nunca receita da CTech.

## Início Rápido

```bash
# Backend
cd api && go test ./... && go run ./cmd/server

# Frontend
cd ui && npm install && npm test && npm run dev

# Infraestrutura
cd cdk && npm install && npx cdk synth
```

## Licença

[Elastic License 2.0 (ELv2)](LICENSE.md) — código fonte disponível, uso como serviço gerenciado por terceiros não
permitido. (Mesma licença dos demais serviços `ctech`.)
