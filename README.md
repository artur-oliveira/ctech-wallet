# CTech Wallet

Serviço de carteira digital do ecossistema `aoctech.app`: um **ledger**, não um gateway de pagamento. Mantém **dois saldos
por usuário** — **real** (dinheiro real; depósito e saque por Pix, feitos pelo `ctech-billing`, que é dono da conexão com
o Inter) e **sandbox** (moeda virtual, sem valor monetário, nunca convertível em real, vendida e gasta no Poker). A carteira
**não** fala com o Inter, não abre cobrança, não vende produto e não conhece regra de jogo: consentimento, limites pessoais e
autoexclusão são do `ctech-poker`. A antiga carteira `game` foi fundida em `real` (2026-10). Serve de base para cobrança de
assinaturas e para apostas de habilidade em poker/dominó, que apostam o saldo `real` por reservas (holds).
Especificação: `docs/specs/2026-10-14-wallet-ledger-billing-rail.md` no `ctech-billing`.

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
`api/internal/oauthresource/scope-manifest.json`, atualmente com 6 permissões
públicas `wallet:*` e 6 permissões M2M `internal:wallet:*` ativas (as demais ficam `deprecated`). O teste de contrato
compara o manifesto com as constantes realmente usadas pelo middleware. O registro do CTech Account recusa remover uma permissão ativa sem que ela tenha sido publicada antes como `deprecated`; por isso as permissões das rotas aposentadas (`wallet:custody:write`, as de compra, as de jogo e jogo responsável) permanecem no manifesto como `deprecated` (não são mais aplicadas) até uma mudança posterior removê-las. O deploy
o publica no CTech Account depois do CDK e antes da API usando um client
confidencial vinculado somente ao Resource Server `wallet`; o papel OIDC lê
apenas os três parâmetros necessários.

`GET /.well-known/oauth-protected-resource` implementa RFC 9728 e anuncia apenas
os 6 scopes públicos ativos. Tokens delegados que carregam qualquer `wallet:*` são
limitados ao scope exato de cada rota; os scopes internos nunca são anunciados.
A UI solicita os 6 scopes públicos junto de `openid profile kyc`. Durante a migração,
tokens first-party já emitidos sem `wallet:*` mantêm o acesso atual. Publicar
o manifesto também acrescenta os scopes públicos ao `allowed_scopes` do OAuth
Client first-party `wallet`, sem alterar redirects, audience ou outros clients
(consulte `OPERATIONS.md`).

## Documentação

| Documento                                                                              | Descrição                                              |
|----------------------------------------------------------------------------------------|--------------------------------------------------------|
| `docs/specs/2026-10-14-wallet-ledger-billing-rail.md` (no repo `ctech-billing`)          | Fonte de verdade atual: o que a carteira é hoje        |
| [`docs/specs/2026-07-10-wallet-design.md`](docs/specs/2026-07-10-wallet-design.md)     | Design original (histórico, anterior ao Billing)       |
| [`docs/legal/wallet-terms-addendum.md`](docs/legal/wallet-terms-addendum.md)           | Aditivo aos Termos de Uso (rascunho)                   |
| [`docs/plans/2026-10-07-account-deletion-participant.md`](docs/plans/2026-10-07-account-deletion-participant.md) | Exclusão de conta (LGPD): participante da saga |
| [`CLAUDE.md`](CLAUDE.md)                                                                | Instruções para Claude Code                            |
| [`AGENTS.md`](AGENTS.md)                                                                | Contexto para agentes de IA (mesmo conteúdo do `CLAUDE.md`) |
| Padrão da família CTech                                                                | skill `ctech-dev-standards` (repo `ctech-skills`)      |

## Subprojetos

```
api/          # Backend REST — Go (Fiber v3), DynamoDB, Valkey, Reconcile; o Pix passa pelo ctech-billing
ui/           # Frontend — Next.js 16 + TypeScript (export estático, Cloudflare Workers Static Assets)
cdk/          # Infraestrutura AWS — CDK TypeScript
rpc-contract/ # Constantes de dinheiro compartilhadas (api e ui): money.json
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
- **Evento do Billing nunca é fonte de verdade** — o depósito só credita depois que a carteira relê a fatura no Billing e confere valor, CPF do pagador e `payer_match`; divergência quarentena e reembolsa.
- **Saque só para o CPF do KYC** — o cliente nunca informa o destino; saque exige `kyc_level == enhanced` + step-up MFA, e um
  saque cuja chamada ao Billing falha fica `processing` até a reconciliação resolver (erro de consulta nunca reverte).
- **Sandbox nunca vira real, real nunca vira sandbox** — a lista de rotas que escrevem é fechada e testada.
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

**Sair de um único produto (`scope: service`).** O ctech-account deixa a pessoa sair de um produto CTech sem encerrar a conta.
A mensagem chega sem workspaces, então o purge apaga só os dados da própria pessoa e deixa os dados dos workspaces; o mesmo
consumer limpa o tombstone com `user.relinked` quando ela volta (`api-commons` ≥ v1.28.0, `Store.Relink`). Nada neste repo muda
por isso. Spec: ctech-account `docs/specs/2026-10-17-service-unlink.md`.

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
