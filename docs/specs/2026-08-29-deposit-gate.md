# Gate de depósito antecipado (KYC + custódia Asaas)

**Data:** 2026-08-29
**Status:** implementado

## Problema

O usuário digitava um valor, clicava em "Depositar" e recebia um alerta:

```json
{"type": "/problems/kyc-not-verified", "status": 403,
 "detail": "verificação de identidade necessária para esta operação"}
```

Sem saída: nenhum atalho para a verificação, nenhuma indicação do que fazer. Com a custódia Asaas ligada há um
segundo beco — `409 /problems/wallet-onboarding`, que o frontend nem conhecia (não estava no mapa de erros, não
havia rota de onboarding em `ui/src`). Passar o KYC levava direto ao próximo bloqueio.

## Princípio

**O botão é o próximo passo.** Um usuário nunca deve digitar um valor num fluxo que já se sabe que vai ser
recusado.

## Backend

### `DepositReadiness` (`api/internal/services/wallet.go`)

Pré-voo somente leitura que avalia exatamente os mesmos portões que `POST /wallet/deposits` aplica — passando
pelo próprio `requireCustodyApproved`, não por uma segunda cópia da regra. Não abre cobrança, não abre subconta,
não muda estado.

```go
type DepositReadiness struct {
    Allowed         bool   `json:"allowed"`
    BlockedBy       string `json:"blocked_by,omitempty"`
    KYCLevel        string `json:"kyc_level"`
    CustodyRequired bool   `json:"custody_required"`
    CustodyStatus   string `json:"custody_status,omitempty"`
}
```

`blocked_by` ∈ `kyc` | `custody_absent` | `custody_pending` | `custody_blocked`.

**A barreira de KYC é a da rota, não a do método.** `POST /wallet/deposits` carrega
`RequireKYC(KYCVerified)` — `enhanced`, sempre, com ou sem custódia. O `kycLevel != ""` interno de
`InitiateDeposit` é mais frouxo e nunca chega a rodar para um usuário `basic`: o middleware já respondeu 403.
Relatar "basic basta" mandaria o usuário para um beco.

Mapa de status de subconta → próximo passo (`custodyBlockReason`):

| Status                                              | `blocked_by`       | Por quê |
|-----------------------------------------------------|--------------------|---------|
| ausente (sem linha)                                  | `custody_absent`   | o único caso que o próprio usuário resolve |
| `onboarding`, `pending_documents`, `pending_approval` | `custody_pending`  | nada a fazer além de esperar |
| `frozen`, `closing`, `subaccount_closed`, `closed`   | `custody_blocked`  | nunca é um passo de onboarding |
| `approved` recusado                                  | `custody_blocked`  | conservation drift (Invariante #13) — nunca é onboarding |

### `GET /v1.0/auth/me`

Ganha o bloco `deposit`. É o request que o `ProtectedRoute` já faz em toda sessão — custo zero de round-trip.

**Falha aberta:** se a sondagem em si falhar (repositório/provedor), o campo é **omitido** e o erro é logado.
Ausente significa "desconhecido, comporte-se como antes" — nunca "bloqueado". Uma leitura transitória com
problema não pode tirar o depósito de quem está perfeitamente onboardado; `InitiateDeposit` continua sendo a
borda que aplica de verdade.

## Frontend

### Estados do card `real`

| Estado | Ação | Linha de apoio |
|---|---|---|
| liberado | `Depositar` | — |
| `kyc` | `Verificar identidade` → `accounts.aoctech.app/account/identity` | "Depósitos liberam após a verificação completa de identidade: documento e selfie." |
| `custody_absent` | `Abrir conta de pagamento` → dialog | "Falta um passo para você receber PIX nesta carteira." |
| `custody_pending` | `Análise em andamento` (desabilitado) | "Sua conta de pagamento está em análise. Avisamos assim que liberar." |
| `custody_blocked` | `Falar com o suporte` | "Depósitos indisponíveis nesta conta." |

O botão de gate ocupa a mesma posição, forma e variante do "Depositar" — muda o rótulo, não a gramática do card.
`Sacar` não é tocado: tem portão próprio (step-up MFA). A nota carrega `id="deposit-gate-note"` e o botão a
referencia por `aria-describedby`, então o estado desabilitado se explica também para leitor de tela.

### `resolveDepositGate` (`ui/src/lib/utils/deposit-gate.ts`)

Função pura, testada, que traduz a resposta do servidor em estado de UI. **Nunca** re-deriva o motivo a partir de
`kyc_level`/`custody_status`: a API é dona do portão, e uma segunda cópia da regra no cliente é uma cópia que vai
divergir. `blocked_by` desconhecido ou `readiness` ausente ⇒ `allowed`.

### Onboarding da conta de pagamento

`OnboardingDialog` coleta um único campo — `income_value` (centavos), cadastral, enviado direto e não persistido
pela wallet — e chama `POST /v1.0/wallet/onboarding`. Sucesso invalida `QUERY_KEY_ME`, e o card cai em
"análise em andamento" na mesma renderização. Copy fala "conta de pagamento", nunca "subconta Asaas": o usuário
não conhece o provedor e não deveria precisar conhecer.

### Espera pela aprovação

A aprovação chega por webhook do Asaas e o BaaS **não** faz broadcast por WebSocket. Em vez de criar um canal
novo só para isso, o dashboard usa `refetchInterval` de 30s no query de `/me` **apenas** enquanto
`blocked_by === 'custody_pending'`. Nenhum outro estado faz polling.

### Rede de segurança

O gate é pré-voo, não aplicação. Um `/me` uma batida desatualizado ainda pode deixar um usuário bloqueado clicar.
Por isso o toast de erro de depósito continua existindo, agora com ação: `kyc-not-verified` ganha o botão
"Verificar identidade", e tanto ele quanto `wallet-onboarding` invalidam `QUERY_KEY_ME` para o card se corrigir.
Novas chaves de erro: `errors.walletOnboarding`, `errors.accountBlocked`.

## Testes

- `TestDepositReadiness` (tabela: sem custódia/com custódia × `""`/`basic`/`enhanced` × ausente/em análise/
  congelada/aprovada/fora da allowlist) — `api/internal/services/wallet_test.go`.
- `TestDepositReadinessOpensNothing` — a sondagem não abre cobrança nenhuma.
- `ui/src/lib/utils/deposit-gate.test.mjs` — inclui os dois casos de falha aberta e o de "nunca re-derivar".

## Componentes revisados

`api` (serviço, handler, docs de endpoint) ↔ `ui` (card, dialog, i18n pt-BR/en, mock) ↔ `ctech-account` (destino
do link de verificação: `ui/src/app/account/identity/page.tsx` — nenhuma mudança necessária lá) ↔ `cdk` (nenhuma:
sem rota nova, sem env nova, sem scope novo — `wallet:custody:write` já era solicitado).
