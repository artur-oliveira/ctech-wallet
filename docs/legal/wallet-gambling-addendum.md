# Termo de Jogo Responsável — CTech Wallet

**Versão:** 1.0
**Atualizado em:** 12 de julho de 2026

> ⚠️ **PENDENTE DE REVISÃO JURÍDICA.** Este texto foi redigido pela equipe de engenharia para descrever com
> precisão o que o sistema faz. Ele **não** foi revisado por um advogado. Antes de entrar em produção
> (`GAMBLING_ENABLED=true`), precisa passar por revisão jurídica — em especial as seções sobre limites, prazos de
> espera e a natureza não conversível dos créditos sandbox.
>
> A versão renderizada para o usuário está em `ui/src/app/gambling-addendum/page.tsx` e **deve ser mantida em
> sincronia com este arquivo e com `wallet.CurrentGamblingAddendumVersion`** (`api/internal/domain/wallet/user.go`).
> Alterar o texto de forma relevante exige subir a versão nos três lugares — subir a constante re-exige o aceite de
> todos os usuários.

Este termo é **específico para o uso da carteira em jogos** e complementa os Termos Adicionais da CTech Wallet
(`docs/legal/wallet-terms-addendum.md`). Aceitar os Termos Adicionais **não** significa aceitar este termo: são
documentos separados, e ele só é necessário para quem quiser usar a carteira para jogos.

## 1. A carteira de jogo

Ao ativar, é criada uma **carteira de jogo** separada do saldo real. O dinheiro na carteira de jogo **continua
sendo dinheiro real e do usuário**: pode ser devolvido ao saldo real a qualquer momento, sem taxa e sem limite, e
de lá sacado por PIX normalmente.

A carteira de jogo existe para separar o que o usuário destinou a jogos do dinheiro que usa para assinaturas e
serviços. É a **única** porta pela qual dinheiro real chega aos jogos: não é possível jogar nem comprar créditos
direto do saldo real.

## 2. Limites pessoais

O usuário define **quanto pode transferir do saldo real para a carteira de jogo** por dia, por semana e por mês. O
limite diário não pode ser maior que o semanal, e o semanal não pode ser maior que o mensal.

O limite conta o **total enviado** no período. Devolver dinheiro da carteira de jogo para o saldo real **não**
libera limite de volta — caso contrário o limite não limitaria nada (bastaria enviar, devolver e enviar de novo).

**Reduzir um limite vale imediatamente. Aumentar um limite só passa a valer depois de um prazo de espera:** 7 dias
para os limites diário e semanal, 14 dias para o mensal. O usuário nunca precisa esperar para se proteger mais —
apenas para se expor mais.

## 3. Créditos sandbox

Créditos sandbox são comprados com o saldo da **carteira de jogo** e servem para participar de partidas em jogos de
habilidade integrados.

**Créditos sandbox não têm valor em dinheiro, não podem ser sacados e não podem ser convertidos de volta em
dinheiro** — nem para a carteira de jogo, nem para o saldo real. A conversão é definitiva e não tem volta.

## 4. Quem pode ativar

É necessário ter 18 anos ou mais e ter concluído a verificação de identidade (KYC verificado) da conta CTech.

A ativação é **opcional**. Quem usa a carteira apenas para assinaturas e serviços não precisa dela, e nada
relacionado a jogos aparece na carteira.

## 5. Jogue com responsabilidade

Jogos com dinheiro podem causar dependência. Aposte apenas o que pode perder, e use os limites — eles existem para
isso.

Se o jogo deixou de ser diversão, procure ajuda:

- **CVV** — 24 horas, telefone **188**, [cvv.org.br](https://www.cvv.org.br)
- **Jogadores Anônimos** — [jogadoresanonimos.com.br](https://jogadoresanonimos.com.br)

## 6. Registro do aceite

O aceite deste termo e a ativação da carteira de jogo são registrados em log de auditoria imutável (`wallet_audit`),
com data, hora, IP e user-agent. O registro é apenas acrescentado, nunca alterado nem apagado.
