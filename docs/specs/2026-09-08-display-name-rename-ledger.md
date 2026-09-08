# Customer-facing product name: "CTech Wallet" → "CTech Ledger"

**Data:** 2026-09-08
**Status:** implementado

## Problema

Asaas (nosso provedor BaaS/PIX) enviou o playbook de compliance de nomenclatura para integradores de
tecnologia sobre rails regulados: instituições não licenciadas não podem se apresentar ao usuário final
usando termos como "Wallet", "Bank", "Pay" ou "Financeira", pois isso pode sugerir que a empresa é uma
instituição financeira licenciada. Não somos — a CTech é uma integradora de tecnologia sobre os rails
regulados da Asaas.

## Solução

Renomeação **puramente visual** do nome de exibição do produto, de "CTech Wallet" para "CTech Ledger", em
todo texto voltado ao usuário final: metadados de página (`<title>`, Open Graph, Twitter card), a marca
exibida no topbar e na tela inicial, e as strings de i18n (`en.json`, `pt-BR.json`), incluindo o
`display_name` mostrado na tela de consentimento OAuth (`api/internal/oauthresource/scope-manifest.json`).

## Fora de escopo (deliberadamente não alterado)

- DNS, hostnames, URLs (`wallet.aoctech.app` etc.), variáveis de ambiente.
- Nomes/descrições de stacks CDK, nomes de workflows do GitHub Actions.
- `README.md`, `OPERATIONS.md`, `ui/DESIGN.md`, `ui/.impeccable/design.json`.
- Qualquer arquivo em `docs/specs/` ou `docs/plans/` além deste (histórico de engenharia).
- Qualquer arquivo em `docs/legal/` (versionamento dos termos é tratado separadamente).
- `ui/public/site.webmanifest` e o teste que o valida (`ui-performance.test.mjs`).
- O identificador interno `resource_server_id: "wallet"`, tabelas/atributos DynamoDB, nomes de pacotes,
  rotas, escopos (`wallet:*`, `internal:wallet:*`) e qualquer outro nome técnico interno.

Nenhuma infraestrutura, contrato de API, schema ou comportamento mudou — apenas o texto exibido ao usuário.

## Selo de atribuição Asaas

Mesmo playbook, seção de transparência ao cliente final (Resolução Conjunta nº 16/2025): o usuário precisa
ser informado de que a Asaas — não a CTech — é a instituição de pagamento que processa a operação, em
toda tela de criação de conta ou movimentação de valores. Componente novo `AsaasBadge`
(`ui/src/components/wallet/asaas-badge.tsx`), um link para `asaas.com` com o selo oficial hospedado pela
Asaas (variante colorida em fundos claros, variante branca sobre o card violeta sólido — a única legível
ali), adicionado em `onboarding-dialog.tsx` (criação da conta de pagamento), `balance-cards.tsx` (card do
saldo real) e `amount-dialog.tsx` (apenas nos fluxos `deposit`/`withdraw`, que são os únicos que tocam a
Asaas) e no footer da home (`components/home.tsx`). Novas chaves i18n: `common.asaasBadgeAlt` em
`en.json`/`pt-BR.json`.

O selo é carregado por URL externa (`baas.asaas.com`), então a CSP `img-src` (gerada pelo workflow
reutilizável de `ctech-cdk`, que por padrão só permite `'self' data:`) precisa do override
`img-src 'self' data: https://baas.asaas.com` em `.github/workflows/frontend.yml`
(mesmo mecanismo já usado pelo poker para os avatares — ver comentário em
`ctech-cdk/.github/workflows/frontend-cloudflare.yml`).
