# Descrição opcional em lançamentos e compras

**Data:** 2026-08-29
**Status:** implementado

## Problema

O extrato só mostrava o tipo do lançamento (`game_credit`, `billing_debit`, …) e a `ref` — uma chave
semântica opaca (`daily_reward`, um id de mesa, um id de fatura). O usuário via *que* houve movimento,
nunca *por quê*. Mesma lacuna em `sandbox_purchases` e `product_purchases`, onde a linha era só o SKU.

## Solução

Um atributo opcional `description` — texto livre, até **255 caracteres** (`wallet.DescriptionMaxLen`) —
que o serviço chamador preenche e a wallet devolve para exibição.

### Regras (não negociáveis)

1. **É metadado de exibição.** Nunca é lido, parseado, comparado ou usado para autorizar/derivar valor,
   crédito, preço ou status.
2. **Fica FORA do request hash de idempotência** (`ReqHash`). Repetir a mesma `idempotency_key` com uma
   descrição diferente devolve o lançamento original — nunca `409 idempotency-conflict`, nunca uma
   segunda linha no ledger. O texto persistido é o da primeira chamada.
3. **Não substitui `ref`.** `ref` continua sendo a chave legível por máquina; `description` é a frase
   legível por humano. As duas coexistem na mesma linha.
4. **Continua sendo append-only.** Uma descrição é gravada junto com o lançamento e nunca atualizada
   depois (Invariante Financeira #2).
5. **Opcional em todo lugar.** Ausente ⇒ atributo ausente no DynamoDB (`omitempty`) e ausente no JSON.
   Linhas históricas não têm o campo e continuam válidas.

## Superfície de API

| Rota                                             | Campo novo no body        |
|--------------------------------------------------|---------------------------|
| `POST /v1.0/internal/wallet/sandbox/credit`       | `description` (≤255)      |
| `POST /v1.0/internal/wallet/sandbox/debit`        | `description` (≤255)      |
| `POST /v1.0/internal/wallet/real/debit`           | `description` (≤255)      |
| `POST /v1.0/internal/wallet/sandbox-purchase/`    | `description` (≤255)      |
| `POST /v1.0/internal/wallet/product-purchase/`    | `description` (≤255)      |
| `POST /v1.0/internal/wallet/charge`               | `description` (≤255)      |

Validação: `validate:"max=255"` no DTO — a fronteira de confiança é o handler; acima do limite é
`400`, nunca truncamento silencioso.

Leitura: `description` aparece em `GET /wallet/:type/ledger`, `GET /wallet/sandbox/purchases` e
`GET /wallet/product-purchases`.

A rota de compra sandbox iniciada pelo próprio usuário (`POST /wallet/sandbox/purchases`) **não** aceita
`description`: não há serviço chamador para descrever a venda, e o SKU já diz o que foi comprado.

## Persistência

Atributo `description` em `wallet_ledger_entries`, `wallet_sandbox_purchases` e
`wallet_product_purchases`. Sem índice, sem migração: atributo opcional em tabela schemaless.

## Frontend

- **Extrato:** quando há descrição, ela é a linha principal da row e o rótulo do tipo desce para a linha
  secundária junto do timestamp (`Tipo · 29 ago 11:57`). Sem descrição, o layout anterior é preservado
  na íntegra. `line-clamp-2` impede que 255 caracteres quebrem a altura da lista.
- **Compras:** descrição entra como linha própria entre o SKU/status e o timestamp.

## Testes

`TestLedgerDescriptionPersistedAndOutsideIdempotencyHash` (`api/tests/integration/wallet_test.go`):
grava a descrição, confirma que `ref` não é sobrescrita, e replica a mesma chave com texto diferente
verificando que o resultado é o lançamento original, sem conflito e sem crédito duplicado.
