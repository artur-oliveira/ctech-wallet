# CTech Wallet

Serviço de carteira digital do ecossistema `aoctech.app`. Mantém **três saldos por usuário** — **real**
(depósito/saque via PIX, banco parceiro Inter), **game** (dinheiro real cercado só para jogos, com limites de
jogo responsável) e **sandbox** (moeda virtual, sem valor monetário, nunca convertível em real). Dinheiro real
entra no cercado de jogo **somente** via `real → game`. Serve de base para cobrança de assinaturas (futuro
`ctech-dfe`/billing) e para apostas de habilidade em poker/dominó. A superfície de jogo (`game`/`sandbox`) fica
dormente enquanto `GAMBLING_ENABLED=false` (padrão), até a revisão legal do aditivo de jogo.

Consome autenticação (OAuth 2.0 / OIDC, JWT via JWKS) e KYC do [`ctech-account`](../ctech-account). Ledger
append-only, transacional, idempotente, sem saldo negativo.

## Documentação

| Documento                                                                              | Descrição                                              |
|----------------------------------------------------------------------------------------|--------------------------------------------------------|
| [`docs/specs/2026-07-10-wallet-design.md`](docs/specs/2026-07-10-wallet-design.md)     | Design aprovado — ledger, PIX, sandbox, escopos M2M    |
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

## Segurança (sistema financeiro)

Este serviço custodia dinheiro real de terceiros. Invariantes não-negociáveis:

- **Saldo nunca negativo** — `ConditionExpression: balance >= :amount` em todo débito.
- **Ledger append-only** — saldo mora em `wallets` (atômico); `ledger_entries` é auditoria imutável.
- **Idempotência obrigatória** — toda operação exige `Idempotency-Key`; replay retorna o resultado anterior.
- **Uma operação por wallet por vez** — lock via Valkey `SETNX` com TTL curto.
- **Webhook nunca é fonte de verdade** — pagamento só credita após reconsulta ao provedor pelo `txid`.
- **Saque com gate** — `kyc_level == verified` + step-up MFA + CPF da chave PIX destino == CPF do KYC.
- **Taxa por-wallet** — `fee_bps`/`fee_min`/`fee_max` opcionais por carteira (default 2% / R$1 / R$10), piso
  absoluto de R$1, configurável só por admin direto no DynamoDB (sem rota de API).

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
