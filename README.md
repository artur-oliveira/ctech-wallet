# CTech Wallet

Serviço de carteira digital do ecossistema `aoctech.app`. Mantém **três saldos por usuário** — **real**
(dinheiro real; depósito/saque PIX desligados até um novo provedor de custódia — ver
`docs/specs/2026-10-07-asaas-removal.md`), **game** (dinheiro real cercado só para jogos, com limites de
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
`api/internal/oauthresource/scope-manifest.json`, atualmente com 12 permissões
públicas `wallet:*` e 11 permissões M2M `internal:wallet:*`. O teste de contrato
compara o manifesto com as constantes realmente usadas pelo middleware. O deploy
o publica no CTech Account depois do CDK e antes da API usando um client
confidencial vinculado somente ao Resource Server `wallet`; o papel OIDC lê
apenas os três parâmetros necessários.

`GET /.well-known/oauth-protected-resource` implementa RFC 9728 e anuncia apenas
os 12 scopes públicos. Tokens delegados que carregam qualquer `wallet:*` são
limitados ao scope exato de cada rota; os scopes internos nunca são anunciados.
A UI solicita os 12 scopes junto de `openid profile kyc`. Durante a migração,
tokens first-party já emitidos sem `wallet:*` mantêm o acesso atual. Publicar
o manifesto também acrescenta os scopes públicos ao `allowed_scopes` do OAuth
Client first-party `wallet`, sem alterar redirects, audience ou outros clients
(consulte `OPERATIONS.md`).

## Documentação

| Documento                                                                              | Descrição                                              |
|----------------------------------------------------------------------------------------|--------------------------------------------------------|
| [`docs/specs/2026-07-10-wallet-design.md`](docs/specs/2026-07-10-wallet-design.md)     | Design aprovado — ledger, PIX, sandbox, escopos OAuth  |
| [`docs/legal/wallet-terms-addendum.md`](docs/legal/wallet-terms-addendum.md)           | Aditivo aos Termos de Uso (rascunho)                   |
| [`docs/plans/2026-10-07-account-deletion-participant.md`](docs/plans/2026-10-07-account-deletion-participant.md) | Exclusão de conta (LGPD): participante da saga |
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
- **Sem rail de depósito/saque** — o provedor de custódia foi removido; nenhum dinheiro de usuário entra na conta
  Inter da CTech. Um rail futuro deve custodiar sob o CPF do próprio usuário e exigir `kyc_level == enhanced` +
  step-up MFA + CPF da chave PIX destino == CPF do KYC no saque.
- **Sem taxa de saque** — o saque debita exatamente o `amount` e grava um único lançamento `withdraw`
  (`docs/specs/2026-08-16-withdrawal-fee-removal.md`). Tarifa de provedor de pagamento é custo de provedor,
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

## Exclusão de conta: toda feature nova entrega seu fluxo de exclusão

A ctech-account orquestra um saga de exclusão (LGPD) em toda a família CTech (specs e revisão em
`ctech-account/docs/specs/2026-10-06-account-deletion-*.md` e `docs/2026-10-13-account-deletion-plan-review.md`).
Cada serviço é participante. Os planos de participante envelheceram conforme features novas entraram, então vale esta regra:

**Uma feature que guarda ou deriva dado ligado a uma pessoa ou organização (tabela, índice, prefixo de chave ou de bucket,
chave de cache, linha de fila ou job, registro em terceiro, campo de auditoria) só está pronta quando o fluxo de exclusão
dela existe e foi testado.** Na mesma alteração:

1. Inclua a linha dela no inventário de dados do serviço (apagar, anonimizar ou reter, com base legal e prazo; reter exige motivo).
2. Implemente o passo de apagar ou anonimizar no purge do serviço, idempotente e retomável (reexecutável do zero).
3. Crie ou atualize os bloqueios de elegibilidade se ela puder guardar dinheiro, dívida, trabalho em aberto ou titularidade.
4. Todo caminho assíncrono (webhook, job, varredura, consumidor de fila) deve descartar trabalho de `sub` ou organização travada ou apagada.
5. Teste: crie os dados, rode o purge duas vezes, confirme que sumiram ou foram anonimizados e que uma escrita tardia para o titular
   apagado é rejeitada. O teste de classificação de tabelas do purge deve falhar para tabela não classificada; mantenha-o exaustivo.
6. Revogue ou apague o que vive em terceiros (por exemplo webhooks ou credenciais registradas em banco).
7. Atualize o plano ou spec do participante e registre o impacto entre serviços (quem mais precisa agir quando isto for apagado).

Um pull request que adiciona dado ligado a pessoa sem isso está incompleto, independentemente do resto passar.

## Licença

[Elastic License 2.0 (ELv2)](LICENSE.md) — código fonte disponível, uso como serviço gerenciado por terceiros não
permitido. (Mesma licença dos demais serviços `ctech`.)
