# Remoção da integração Asaas (BaaS de custódia)

> **Atualizado 2026-10-08:** o rail Inter de depósito e saque foi restaurado; ver `2026-10-08-wallet-restoration-design.md`.

**Data:** 2026-10-07
**Status:** implementado (branch `chore/remove-asaas`)

## Por quê

A Asaas recusou a solicitação de conta BaaS da CTech. Sem provedor de custódia não existe rail legítimo para
depósito/saque PIX da carteira `real`: a Invariante #12 proíbe que dinheiro de usuário entre na conta Inter da
CTech. Nada da integração Asaas chegou a rodar em produção, então a remoção não migra nem estorna dados.

## O que foi removido

- **Rotas:** `POST /v1.0/wallet/deposits`, `POST /v1.0/wallet/withdrawals`, `GET`/`POST /v1.0/wallet/onboarding`,
  `POST /v1.0/wallet/closure`, `POST /v1.0/internal/asaas/webhook`, `POST /v1.0/internal/asaas/transfer-authorization`.
- **Campos:** `deposit` (`DepositReadiness`) em `GET /v1.0/auth/me`; `custody_status` em `GET /v1.0/wallet/`.
- **Scope:** `wallet:custody:write`. `wallet:deposits:write`/`wallet:withdrawals:write` seguem declarados, sem rota.
- **Problem types:** `wallet-onboarding`, `account-blocked`, `med-receivable-open`, `deposit-receipts-exhausted`,
  `custody-fee-unpaid`.
- **Config:** todo `ASAAS_*` e os parâmetros SSM `/ctech-wallet/{env}/asaas/*`.
- **Lógica:** serviço BaaS, taxa de verificação/custódia, clawback MED, transfer intents, conservation check,
  sweep de onboarding, liquidação/estorno de compra de jogo (`ReverseSandboxGamePurchase`, ledger types
  `sandbox_purchase_reversal`/`game_fund_reversal`), franquia mensal de recibos PIX, fluxo de step-up na UI.
- **Docs** Asaas-específicos (design de custódia, plano, auditoria jurídica, rollout, gate de depósito, depósitos
  só-Asaas) — o histórico fica no git.

## O que permanece

- Inter (legado): `POST /internal/pix/confirm-deposit` + `ConfirmDeposit` (re-consulta, match de CPF, estornos)
  para linhas de depósito Inter antigas; sweeps do reconcile de depósitos pendentes, estornos de depósito e saques
  em `processing` (`QueryTransfer`).
- Compras diretas de sandbox, compras de produto e cobrança M2M via Inter.
- Ring-fence `real ↔ game`, holds, sandbox, limites de jogo responsável.
- `middleware.RequireRecentMFA` existe, mas nenhuma rota o usa.

## Invariantes

- **#12** passa a ser: dinheiro de usuário nunca cai na conta Inter da CTech; hoje não há rail de depósito nem
  de saque.
- **#13** (taxa de verificação BaaS) foi aposentada; a numeração é mantida porque o código cita #11/#12/#14.
- **#11** perde a menção a webhooks de onboarding; o texto do webhook Inter fica.

## Tabelas aposentadas (CDK em 2 fases)

`wallet_baas_accounts`, `wallet_transfer_intents`, `wallet_med_receivables`, `wallet_settlement_legs`:

1. **Este PR:** continuam declaradas com `RETAIN` e sem acesso IAM; o GSI `gsi_deposit_provider_qr` foi removido.
2. **PR seguinte:** remove os constructs; as tabelas ficam órfãs na conta AWS, com os dados preservados.

## O que um provedor futuro precisa reintroduzir

- Custódia sob o **CPF do próprio usuário** (subconta ou equivalente) — nunca a conta Inter da CTech.
- Crédito somente após **re-consulta** ao provedor; webhook é só sinal de "acorde e verifique" (Invariante #11).
- Rotas de depósito e saque, com idempotência, lock e o gate de faixa de depósito antes de abrir cobrança.
- Saque com KYC `enhanced`, CPF da chave destino == CPF do KYC e **step-up MFA** (`RequireRecentMFA` +
  `max_age=0` no `ctech-account`).
- Estado `processing` resolvido pelo reconcile (Invariante #14).
