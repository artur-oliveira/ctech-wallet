# Auditoria jurídica preventiva — Asaas BaaS + jogos reais (CTech Wallet/Poker)

**Data:** 2026-07-30
**Referente a:** `docs/specs/2026-07-29-asaas-baas-custody-design.md` (cujo próprio cabeçalho já previa esta revisão em
"Legal review: 2026-07-30")
**Natureza:** revisão preventiva de engenharia com apoio de pesquisa jurídica direta em fontes oficiais. **Não é
parecer jurídico.** Não substitui a manifestação assinada de advogado(a) inscrito(a) na OAB com experiência em
meios de pagamento, direito do consumidor, PLD/FT e jogos. Toda conclusão abaixo é hipótese para confirmação do
parecerista, não autorização para operar.
**Contexto declarado pelo usuário:** já existe consulta formal protocolada à SPA (Secretaria de Prêmios e
Apostas) — ver §4.

---

## 0. Sumário executivo

O modelo técnico revisado em `2026-07-29-asaas-baas-custody-design.md` está, em sua quase totalidade, **corretamente
fundamentado**: as citações de Resolução Conjunta BCB/CMN nº 16/2025 confirmadas nesta auditoria bateram
**100%** com o texto oficial, sem nenhum erro de artigo ou número. Isso é raro e vale registrar — a camada de
custódia (segregação de saldo do usuário em subconta própria no Asaas) tem base legal sólida e verificada.

Os pontos que este documento adiciona ou corrige em relação ao spec de 2026-07-29 são:

1. **O art. 21-A da Lei 14.790/2023 existe e tem texto localizado** — não fazia parte da lei original de 2023;
   foi inserido pela **Lei nº 15.358/2026** (24/03/2026) e regulamentado pelo **Decreto nº 13.033/2026**
   (19/06/2026), instituindo um mecanismo de bloqueio de contas ("asfixia financeira") para operador não
   autorizado de apostas de quota fixa. Isso eleva o risco sistêmico de uma classificação desfavorável: não é só
   "não pode lançar" — é "as instituições de pagamento (Asaas) são **obrigadas** a bloquear as contas dos
   usuários" se a SPA algum dia classificar o produto como quota fixa não autorizada. Ver §3.3.
2. **A consulta já enviada à SPA (protocolo 18800.127906/2026-68) tem efeito jurídico mais limitado do que o
   pedido presume**, e o texto da própria petição contém uma afirmação que a pesquisa não conseguiu verificar
   (citação a decisão do STJ). Ver §4 — isto merece atenção antes de qualquer decisão de lançamento baseada na
   expectativa de resposta da SPA.
3. **A base legal para o netting multilateral de mesa (§5.4.2 e pergunta 14.2.6 do spec) está mal fundamentada.**
   Compensação do Código Civil (art. 368) é estritamente bilateral. Uma liquidação líquida entre 3+ jogadores não
   se encaixa em "compensação legal" — precisa de outra base contratual. Ver §5.2.
4. **Duas citações do Código Civil no spec original merecem correção de precisão**: a regra de "pagamento
   voluntário de dívida de jogo não é repetível" está no **caput do art. 814**, não no art. 815 (que trata de
   empréstimo para jogar). Ver §5.1.
5. **CP art. 168 tem um agravante específico (§1º, III) para quem se apropria em razão de ofício/profissão** —
   diretamente aplicável ao papel de custodiante que o mandato dá à CTech. O parecer de counsel deve endereçar
   isso, não só o caput. Ver §5.3.

Nada abaixo altera a conclusão operacional já correta do spec: **jogo real permanece bloqueado** até parecer
assinado de counsel sobre classificação (habilidade x azar, quota fixa) e sobre a executabilidade civil do
resultado (art. 814 CC). A camada de custódia BaaS pode seguir, sob as premissas já confirmadas no spec §0.1.

---

## 1. Metodologia

Pesquisa dividida em três frentes paralelas (regulação BaaS/pagamentos; jogos/apostas; civil/consumidor/AML/LGPD),
cada uma buscando o texto oficial em `planalto.gov.br`, `bcb.gov.br`, `in.gov.br` ou `gov.br`. Em vários casos o
fetch direto a `planalto.gov.br` sofreu falha de conexão no ambiente de pesquisa; nesses casos o texto foi
corroborado por pelo menos duas fontes secundárias independentes que reproduzem o texto oficial na íntegra
(Jusbrasil, LegJur, Câmara dos Deputados/legin, LegisWeb) — sinalizado abaixo como "verificado por espelho".
Onde o fetch direto funcionou (regulação BaaS), está sinalizado como "verificado na fonte primária". Nenhuma
citação foi inventada; onde não foi possível confirmar, está marcado **NÃO VERIFICADO** explicitamente.
Recomenda-se que o(a) advogado(a) confirme pessoalmente qualquer citação antes de peticionar ou contratar com
base nela — esta auditoria reduz risco de citação incorreta, não o elimina.

---

## 2. Camada de custódia BaaS (Resolução Conjunta BCB/CMN nº 16/2025)

**Verificado na fonte primária** (`bcb.gov.br/estabilidadefinanceira/exibenormativo?tipo=Resolução%20Conjunta&numero=16`).
Todas as oito citações do spec de 2026-07-29 (§0, arts. 3º, IV; 4º, §2º; 8º, XI; 8º, XIV; 8º, §2º, I–II; 14, I;
9º/10/11/16; 8º, §6º, II; 15) **bateram exatamente** com o texto oficial. Nenhum erro de número ou substância
encontrado.

Dois pontos novos, não presentes no spec original:

- **Prazo de adequação contratual: 31/12/2026** (art. 22) para contratos BaaS já existentes. Isso é uma data que
  deveria constar do controle de compliance — se o contrato Asaas–CTech foi assinado antes de 28/11/2025, ele tem
  esse prazo para aderência formal à Resolução.
- **Correção de data, sem impacto de substância:** a Lei 12.865 é de **9 de outubro de 2013**, não de abril. O
  spec de 2026-07-29 não afirma a data (só cita "Lei 12.865/2013"), então não há erro a corrigir ali — registra-se
  aqui só para não propagar a data errada em documento futuro.

Pesquisa não encontrou nenhuma resolução ou instrução normativa de 2026 que substitua ou altere a Res. Conj.
16/2025 até a data desta auditoria — achado de ausência, não exaustivo.

**Fonte:** [Resolução Conjunta BCB/CMN nº 16/2025](https://www.bcb.gov.br/estabilidadefinanceira/exibenormativo?tipo=Resolu%C3%A7%C3%A3o%20Conjunta&numero=16),
[Lei 12.865/2013](https://www.planalto.gov.br/ccivil_03/_ato2011-2014/2013/lei/l12865.htm).

---

## 3. Camada penal/administrativa de jogos — o núcleo do bloqueio

### 3.1 Decreto-Lei 3.688/1941 (Lei das Contravenções Penais), art. 50

**Verificado por espelho.** Caput: contravenção estabelecer ou explorar jogo de azar em lugar acessível ao
público; pena de prisão simples de 3 meses a 1 ano + multa; aumento de 1/3 se há participação de menor. §3º, "a":
jogo de azar é aquele em que "o ganho e a perda dependem exclusiva ou principalmente da sorte". Esse é exatamente
o ponto de virada da tese de defesa (habilidade x sorte) e está corretamente citado no spec §0.2 e §9.4.

**Fonte:** [Decreto-Lei 3.688/1941](https://www.planalto.gov.br/ccivil_03/decreto-lei/del3688.htm).

### 3.2 Lei 14.790/2023 — regime de apostas de quota fixa

**Verificado por espelho** para arts. 2º, II (definição de quota fixa), 4º e 6º (autorização federal obrigatória),
21 (proibição de instituições de pagamento processarem operador não autorizado). Todas batem com a leitura do spec.

**Fonte:** [Lei 14.790/2023](https://www.planalto.gov.br/ccivil_03/_ato2023-2026/2023/lei/l14790.htm).

### 3.3 Art. 21-A — achado novo: existe, mas não como o spec original supôs

O spec de 2026-07-29 cita "art. 21-A as amended in 2026" sem detalhar. A pesquisa confirma que **o artigo não
existia na lei promulgada em 2023** — foi **inserido pela Lei nº 15.358, de 24 de março de 2026** (parte do
"Marco Legal do Combate ao Crime Organizado"), e sua operacionalização foi definida pelo **Decreto nº 13.033, de
19 de junho de 2026**.

O mecanismo ("asfixia financeira"): constatada pela autoridade reguladora/supervisora a exploração de apostas de
quota fixa por pessoa não autorizada, as instituições financeiras e instituições de pagamento **devem**:
(I) bloquear as contas relacionadas; (II) impedir novas transações.

**Por que isso importa mais do que o spec já registrava:** o risco não é só "não lançar sem autorização". É que,
**depois de lançado**, se a SPA (ou outra autoridade competente) em qualquer momento futuro concluir que o
produto é quota fixa disfarçada, o **Asaas é legalmente obrigado a bloquear as subcontas dos usuários** — o
exato "frozen subaccount" que o spec já trata em §5.5, mas agora com base normativa explícita e superveniente à
migração de custódia. Isso não é hipotético: é o gatilho estatutário direto. Recomenda-se que o parecer de
counsel (spec §14, pergunta 15) trate especificamente do art. 21-A/Decreto 13.033/2026 como cenário de risco, não
só da Lei 14.790/2023 em abstrato.

**Fontes:** [Lei 15.358/2026](https://www.planalto.gov.br/ccivil_03/_ato2023-2026/2026/lei/l15358.htm),
[espelho Câmara dos Deputados — Lei 15.358/2026](https://www2.camara.leg.br/legin/fed/lei/2026/lei-15358-24-marco-2026-798846-publicacaooriginal-178585-pl.html),
[espelho Câmara dos Deputados — Decreto 13.033/2026](https://www2.camara.leg.br/legin/fed/decret/2026/decreto-13033-19-junho-2026-799374-publicacaooriginal-180003-pe.html)
— confirmar o link direto em `planalto.gov.br` antes de citar em petição, pois o fetch direto falhou nesta sessão.

### 3.4 Portaria SPA/MF nº 1.207/2024

**Verificado por espelho** (LegisWeb, aviso oficial gov.br/fazenda). Confirma número e data (29/07/2024) e a
exclusão do conceito de "jogo online de quota fixa" para: (i) jogos de habilidade (resultado determinado
principalmente por destreza/conhecimento, mesmo com elemento aleatório não preponderante); (ii) fantasy sports;
(iii) jogos multiapostador; (iv) jogos P2P entre apostadores. Bate com a leitura do spec §9.4 e com o texto da
consulta enviada à SPA.

**Atenção:** uma busca inicial confundiu esta portaria com a **Portaria SPA/MF nº 1.231/2024** (instrumento
diferente, sobre jogo responsável/publicidade). Confirmado que 1.207/2024 é o número correto para a exclusão de
jogos de habilidade/P2P — mas vale o parecerista confirmar o número exato lendo o DOU diretamente, já que a fonte
primária não pôde ser buscada nesta sessão.

**Fontes:** [aviso oficial Ministério da Fazenda](https://www.gov.br/fazenda/pt-br/assuntos/noticias/2024/julho/ministerio-da-fazenda-publica-portaria-com-regras-para-jogos-on-line),
[espelho LegisWeb](https://www.legisweb.com.br/legislacao/?id=462643).

---

## 4. A consulta já protocolada na SPA — o que ela cobre e o que não cobre

Protocolo informado: **18800.127906/2026-68** (Fala.BR/MF, "CONSULTA À SPA — PLATAFORMA DE PÔQUER DE HABILIDADE"),
prazo de atendimento **31/08/2026**.

### 4.1 Efeito jurídico real da consulta

A pesquisa confirmou que a SPA tem um canal normativo de **consulta prévia** (Instrução Normativa SPA/MF nº
35/2025), mas esse instrumento é **voltado a operadores já autorizados** pedindo posição prévia sobre mudança
societária/operacional — não é um procedimento codificado de "enquadramento" para empresa não operadora
perguntando se está dentro ou fora do regime. Isso não invalida o pedido enviado (o protocolo Fala.BR é um canal
administrativo geral legítimo do Ministério da Fazenda), mas significa que a resposta esperada é uma
**manifestação administrativa da SPA sobre a Lei 14.790/2023**, não uma decisão vinculante em processo formal de
licenciamento com efeitos de coisa julgada administrativa.

**Mais importante: a competência da SPA é estritamente sobre o regime de quota fixa (Lei 14.790/2023).** A SPA
**não tem competência** para se manifestar sobre a tipificação do art. 50 do Decreto-Lei 3.688/1941 (contravenção
penal de jogo de azar) — isso é matéria de polícia judiciária/Ministério Público/Poder Judiciário. Uma resposta
favorável da SPA resolve a exposição da Lei de Apostas, mas **não é escudo** contra a tese de jogo de azar
contravencional. A defesa de "predominância de habilidade" continua sendo autônoma e precisa de prova própria
(estatística/pericial), exatamente como o spec já registra em §14.2.13 — este ponto do spec está correto e esta
auditoria o reforça: **não presumir que uma resposta favorável da SPA encerra o bloqueio legal do jogo real**.

### 4.2 Ponto de atenção sobre o próprio texto da petição enviada

A petição afirma: *"O Superior Tribunal de Justiça já decidiu que o pôquer Texas Hold'em é jogo de habilidade."*
A pesquisa dedicada a este ponto (consulta a fontes jurídicas e jornalísticas, 2023–2026 e período anterior) **não
localizou acórdão do STJ (nem do STF) citável por número de processo** que sustente essa afirmação — apenas
comentário doutrinário/jornalístico repetido sem citação de processo verificável (uma nota da PokerNews de 2016
menciona uma decisão não identificada; um texto de opinião do ConJur de 2020 é citado por terceiros sem número de
processo). Isso não significa necessariamente que a afirmação seja falsa — apenas que não foi possível
confirmá-la com uma fonte primária judicial nesta pesquisa.

Como se trata de uma afirmação de fato feita formalmente a um órgão da administração pública federal, recomenda-se:

1. Localizar o acórdão exato (número, relator, data, órgão julgador) antes de qualquer nova comunicação com a
   SPA que repita a alegação; ou
2. Se não for localizável, enviar complemento à consulta já protocolada retificando para "entendimento doutrinário
   consolidado" em vez de "decisão do STJ", evitando expor a empresa a uma alegação de fato imprecisa em processo
   administrativo formal.

### 4.3 Divergência a confirmar entre a petição e o spec técnico

A petição enviada descreve a taxa de acesso como cobrada **"uma vez por nível de mesa e por dia"** (R$1 a R$8).
O spec técnico (`2026-07-29-asaas-baas-custody-design.md` §9.4, §14.1) descreve uma **"taxa fixa de entrada por
tier"** sem explicitar a periodicidade diária. Confirmar que a implementação real (`api/internal/api/v1/stakes.go`)
corresponde exatamente ao que foi declarado à SPA — qualquer divergência entre o que foi formalmente declarado a
uma autoridade e o que está implementado em produção é um risco autônomo (declaração incompleta/imprecisa a
órgão público), independente da classificação de mérito.

### 4.4 Prazo de resposta não gera aprovação tácita

O prazo de 31/08/2026 é uma meta de atendimento do sistema Fala.BR (Lei 13.460/2017 — Código de Defesa do Usuário
do Serviço Público), não um prazo com efeito de silêncio positivo. A pesquisa não localizou dispositivo na Lei
14.790/2023 ou em seu regulamento que preveja aprovação tácita por decurso de prazo. **Não presumir liberação do
produto se a SPA não responder até 31/08/2026** — a ausência de resposta mantém o bloqueio do spec §9.4/§14
integralmente.

**Fontes:** [Lei 14.790/2023](https://www.planalto.gov.br/ccivil_03/_ato2023-2026/2023/lei/l14790.htm),
[Instrução Normativa SPA/MF nº 35/2025 — espelho LegisWeb](https://www.legisweb.com.br/legislacao/?id=487856),
[Lei 13.460/2017](https://www.planalto.gov.br/ccivil_03/_ato2015-2018/2017/lei/l13460.htm).

---

## 5. Camada civil — dívida de jogo, mandato, apropriação indébita

### 5.1 Código Civil art. 814 x art. 815 — correção de precisão

**Verificado por espelho.** A regra de que "pagamento voluntário de dívida de jogo não pode ser reavido" está no
**próprio caput do art. 814**: *"As dívidas de jogo ou de aposta não obrigam a pagamento; mas não se pode
recobrar a quantia, que voluntariamente se pagou, salvo se foi ganha por dolo, ou se o perdente é menor ou
interdito."* §1º estende a regra a contratos que disfarcem, novem ou garantam essa dívida; §2º excepciona jogos
legalmente autorizados; §3º excepciona prêmios de competição esportiva/intelectual/artística que cumpram
requisitos legais.

**O art. 815 trata de outra coisa**: *"Não se pode exigir reembolso do que se emprestou para jogo ou aposta, no
ato de apostar ou jogar."* — é a regra sobre **empréstimo** feito para viabilizar a aposta, não sobre a dívida de
jogo em si. O spec de 2026-07-29 cita os dois artigos juntos na tabela de §0.2 como se governassem o mesmo ponto;
tecnicamente são regras distintas. Isso não muda a conclusão operacional (jogo real bloqueado até parecer sobre
art. 814), mas o parecer de counsel deve tratar o art. 815 separadamente e apenas se a CTech antecipar fundos ao
jogador antes da liquidação (o que o modelo atual, baseado em hold sobre saldo já do jogador, evita — ver spec
§5.4.2).

**Fonte:** [Código Civil — Lei 10.406/2002](https://www.planalto.gov.br/ccivil_03/leis/2002/l10406compilada.htm).

### 5.2 Achado novo: compensação (art. 368 CC) não sustenta o netting multilateral de mesa

O spec §5.4.2 propõe liquidar o fechamento de mesa como um conjunto de transferências **líquidas** (netting):
perdedores líquidos → ganhadores líquidos, com no máximo N-1 transferências para N jogadores em vez de N². A
pergunta 14.2.6 do spec pede ao counsel para "validar a base nos arts. 368–380 do Código Civil" para essa operação.

A pesquisa confirma que **compensação legal no Código Civil é estritamente bilateral**: art. 368 exige que "as
duas partes" sejam "ao mesmo tempo credor e devedor uma da outra" — ou seja, pressupõe exatamente duas partes com
obrigações reciprocamente cruzadas. **Uma operação de netting envolvendo 3 ou mais jogadores na mesma mesa não se
encaixa na figura da compensação legal do CC**, porque não há necessariamente uma obrigação bilateral direta
entre o jogador A (perdedor líquido) e o jogador C (ganhador líquido) que nunca jogaram uma mão um contra o outro
diretamente — apenas posições líquidas resultantes de várias mãos multilaterais.

Isso não torna o netting inválido — mas muda a base jurídica que sustenta sua validade. Não é "compensação do
art. 368", é uma **autorização contratual multilateral** (o regulamento do jogo aceito por todos os participantes
+ o mandato de cada jogador autorizando especificamente a liquidação líquida registrada no livro-razão da CTech,
exatamente como já desenhado na cláusula IV do mandato revisado em §9.2 do spec). A pergunta 14.2.6 deveria ser
reformulada para o counsel: não "isso é compensação válida sob os arts. 368–380?", mas **"o conjunto
mandato + regulamento de jogo aceito por todos os participantes é base contratual suficiente para autorizar a
transferência líquida entre não-contrapartes diretas, sem que isso seja lido como novação disfarçada de dívida de
jogo sob o art. 814, §1º?"** — é uma pergunta mais estreita e mais correta do que a atual.

**Fonte:** [Código Civil, arts. 368–380](https://www.planalto.gov.br/ccivil_03/leis/2002/l10406compilada.htm).

### 5.3 Código Penal art. 168 — o agravante do §1º, III, não só o caput

**Verificado por espelho.** Caput: *"Apropriar-se de coisa alheia móvel, de que tem a posse ou a detenção."*
Pena de reclusão de 1 a 4 anos + multa. O spec cita corretamente o caput em §1 (Problema — custódia). Mas há um
**agravante específico não mencionado no spec**: **§1º, III** aumenta a pena de 1/3 quando a apropriação ocorre
"em razão de ofício, emprego ou profissão". O papel da CTech como mandatária/custodiante profissional dos saldos
dos usuários (posse/detenção de dinheiro de terceiro exatamente em razão de sua atividade empresarial) é o
cenário de fato que esse inciso descreve. Isso não muda se há ou não apropriação — mas, se houver, a pena é maior
do que o caput isolado sugere. Recomenda-se que o parecer trate explicitamente do §1º, III, ao avaliar a exposição
penal da administração de contas de terceiros pela CTech.

**Fonte:** [Código Penal — Decreto-Lei 2.848/1940, art. 168](https://www.planalto.gov.br/ccivil_03/decreto-lei/del2848compilado.htm).

### 5.4 Mandato (CC arts. 653–692) — confirmação sem achado novo

Arts. 653, 654 §1º, 657, 661 §1º, 667, 682 I e II — todos verificados por espelho e batem com a leitura do spec
§9.2. Uma ressalva: o texto exato do art. 668 (prestação de contas) não pôde ser confirmado nesta pesquisa —
recomenda-se que o counsel confirme a redação exata antes de citá-lo em parecer ou contrato.

---

## 6. AML e LGPD — confirmação, com um esclarecimento de base legal

**Lei 9.613/1998**: art. 9º, parágrafo único, VI (entidades que operam sistemas de coleta de apostas e pagamento
de prêmios são "pessoas obrigadas") **confirmado e verificado por espelho** — este é um gatilho de PLD/FT
**independente** da base de intermediação financeira de terceiros (art. 9º, caput, I), reforçando a conclusão do
spec §9.5 de que a CTech pode ter dever próprio de registro/comunicação ao COAF, não delegável ao Asaas. Art. 10
confirma retenção mínima de registros por **5 anos**; art. 11 confirma prazo de comunicação de operação suspeita
ao COAF em **24 horas**, sob sigilo.

**LGPD**: a base legal correta para coleta de biometria no KYC é **art. 11, II, "g"** (prevenção à fraude e
segurança na autenticação), não o art. 7º, V (mera execução de contrato, insuficiente para dado sensível) —
confirmação alinhada ao que o spec já recomendava em §9.7. Art. 18 (direito de eliminação) **não é absoluto**:
pode ser limitado pelas hipóteses de retenção do art. 16 (obrigação legal/regulatória) — ponto relevante para a
política de retenção que o spec já pede em §9.7 e que ainda não existe.

**Fontes:** [Lei 9.613/1998](https://www.planalto.gov.br/ccivil_03/leis/L9613.htm),
[LGPD — Lei 13.709/2018](https://www.planalto.gov.br/ccivil_03/_ato2015-2018/2018/lei/l13709.htm).

---

## 7. Consumidor (CDC) — confirmação sem achado novo

Arts. 6º, III; 14; 46; 49; 51, I/II/IV/§1º; 54, §§3º–4º — todos verificados por espelho, todos batem com a leitura
do spec §9.9. Nenhuma correção a fazer.

**Fonte:** [CDC — Lei 8.078/1990](https://www.planalto.gov.br/ccivil_03/leis/l8078compilado.htm).

---

## 8. Tabela consolidada — o que muda no spec de 2026-07-29

| # | Item | Tipo | Ação recomendada |
|---|------|------|-------------------|
| 1 | Art. 21-A da Lei 14.790/2023 tem origem e texto localizados (Lei 15.358/2026 + Decreto 13.033/2026) | Reforço/detalhamento | Citar a base exata no parecer do counsel (spec §14, pergunta 15); tratar bloqueio de subconta por "asfixia financeira" como cenário concreto em §5.5 |
| 2 | Consulta à SPA não tem efeito de licenciamento vinculante nem de silêncio positivo, e não cobre a contravenção do art. 50 DL 3.688/1941 | Correção de expectativa | Não tratar resposta (ou ausência de resposta) da SPA como liberação do bloqueio de jogo real; manter gate de counsel sobre art. 814 CC e art. 50 DL 3.688/1941 independente da SPA |
| 3 | Petição à SPA cita decisão do STJ não verificável nesta pesquisa | Risco de fato | Localizar o acórdão exato ou retificar a petição antes de nova comunicação com a SPA |
| 4 | Periodicidade da taxa de acesso ("por dia") na petição à SPA pode divergir da implementação | Risco de fato | Confirmar `stakes.go` corresponde exatamente ao declarado à SPA |
| 5 | Compensação do art. 368 CC é bilateral; não sustenta netting de mesa com 3+ jogadores | Correção de fundamento jurídico | Reformular pergunta 14.2.6 do spec: base é mandato + regulamento de jogo, não compensação legal |
| 6 | Art. 814 caput (não art. 815) contém a regra de não-repetição de pagamento voluntário de dívida de jogo | Correção de precisão | Ajustar citação em revisões futuras do spec/parecer |
| 7 | CP art. 168, §1º, III (agravante por ofício/profissão) não mencionado no spec | Complemento | Incluir no escopo do parecer sobre apropriação indébita |
| 8 | Prazo de adequação contratual BaaS até 31/12/2026 (Res. Conj. 16/2025, art. 22) | Informação nova | Confirmar data de assinatura do contrato Asaas–CTech contra esse prazo |

---

## 9. Fontes consultadas (links oficiais e espelhos verificados)

- [Resolução Conjunta BCB/CMN nº 16/2025](https://www.bcb.gov.br/estabilidadefinanceira/exibenormativo?tipo=Resolu%C3%A7%C3%A3o%20Conjunta&numero=16)
- [Lei 12.865/2013](https://www.planalto.gov.br/ccivil_03/_ato2011-2014/2013/lei/l12865.htm)
- [Decreto-Lei 3.688/1941 (Lei das Contravenções Penais)](https://www.planalto.gov.br/ccivil_03/decreto-lei/del3688.htm)
- [Lei 14.790/2023](https://www.planalto.gov.br/ccivil_03/_ato2023-2026/2023/lei/l14790.htm)
- [Lei 15.358/2026](https://www.planalto.gov.br/ccivil_03/_ato2023-2026/2026/lei/l15358.htm) ·
  [espelho Câmara](https://www2.camara.leg.br/legin/fed/lei/2026/lei-15358-24-marco-2026-798846-publicacaooriginal-178585-pl.html)
- [Decreto 13.033/2026 — espelho Câmara](https://www2.camara.leg.br/legin/fed/decret/2026/decreto-13033-19-junho-2026-799374-publicacaooriginal-180003-pe.html)
- [Aviso oficial MF — Portaria SPA/MF 1.207/2024](https://www.gov.br/fazenda/pt-br/assuntos/noticias/2024/julho/ministerio-da-fazenda-publica-portaria-com-regras-para-jogos-on-line) ·
  [espelho LegisWeb](https://www.legisweb.com.br/legislacao/?id=462643)
- [Instrução Normativa SPA/MF nº 35/2025 — espelho LegisWeb](https://www.legisweb.com.br/legislacao/?id=487856)
- [Lei 13.460/2017 (Código de Defesa do Usuário do Serviço Público)](https://www.planalto.gov.br/ccivil_03/_ato2015-2018/2017/lei/l13460.htm)
- [Código Civil — Lei 10.406/2002](https://www.planalto.gov.br/ccivil_03/leis/2002/l10406compilada.htm)
- [Código Penal — Decreto-Lei 2.848/1940](https://www.planalto.gov.br/ccivil_03/decreto-lei/del2848compilado.htm)
- [CDC — Lei 8.078/1990](https://www.planalto.gov.br/ccivil_03/leis/l8078compilado.htm)
- [Lei 9.613/1998 (Lavagem de Dinheiro)](https://www.planalto.gov.br/ccivil_03/leis/L9613.htm)
- [LGPD — Lei 13.709/2018](https://www.planalto.gov.br/ccivil_03/_ato2015-2018/2018/lei/l13709.htm)

---

## 10. Conclusão

O modelo técnico de custódia (Asaas BaaS, subconta por usuário) tem fundamento legal corretamente identificado e
verificado ponto a ponto. O bloqueio de jogo real permanece necessário e agora tem um argumento adicional a favor
de mantê-lo até parecer assinado: a existência confirmada do mecanismo de bloqueio compulsório de contas
(art. 21-A, Lei 15.358/2026 + Decreto 13.033/2026) significa que uma classificação retroativa desfavorável não
apenas impede o lançamento — ela **atinge diretamente os saldos já custodiados dos usuários** depois de operando.
A consulta já protocolada na SPA é um passo de boa diligência, mas sua resposta — favorável ou não — **não
resolve isoladamente** a exposição penal (DL 3.688/1941) nem a executabilidade civil (CC art. 814); ambas
continuam exigindo parecer próprio de counsel, exatamente como o spec de 2026-07-29 já previa em §14.

**Próximo passo recomendado:** levar este documento e o protocolo 18800.127906/2026-68 ao(à) advogado(a)
responsável pelo parecer de §14, junto com a correção da citação do STJ (§4.2) e a confirmação de conformidade
entre a taxa declarada à SPA e a implementação em produção (§4.3).
