---
target: src/app src/components
total_score: 30
p0_count: 0
p1_count: 3
p2_count: 9
timestamp: 2026-07-15T14-00-58Z
slug: src-app-src-components
---
# CTech Wallet — Design Critique (src/app + src/components)

**Method:** dual-agent (A: design review · B: technical/a11y evidence). Deterministic scan (detect.mjs) blocked by environment auto-mode; no browser-automation tool → overlays unavailable. Built on full read of all 20 target files + supporting lib for behavior.

## Design Health Score: 30/40 (Good)

| # | Heuristic | Score | Key Issue |
|---|-----------|-------|-----------|
| 1 | Visibility of System Status | 3 | WS dot + skeletons good; money-move end-state toast-only |
| 2 | Match System ↔ Real World | 3 | BRL/pt-BR/PIX correct; R$ shown on non-money credits flow |
| 3 | User Control & Freedom | 3 | Escape/backdrop/cancel work; no undo (acceptable for money) |
| 4 | Consistency & Standards | 2 | R$ on credits; hand-rolled traps; hardcoded grays/reds bypass tokens |
| 5 | Error Prevention | 4 | Fee/balance/million caps, max, live fee, 2-step commit |
| 6 | Recognition > Recall | 3 | Icons+labels+treatments good; orphaned v1 legal pages |
| 7 | Flexibility & Efficiency | 3 | max shortcut, Tab trap; no deposit hotkey, no saved PIX keys |
| 8 | Aesthetic & Minimalist | 3 | Restrained, on-brand; auth/consent shell generic |
| 9 | Help & Documentation | 3 | Legal linked; no in-context "how limits work" |
| 10 | Error Recovery | 3 | Strong RFC7807→i18n; errors are auto-dismissing toasts only |

## Anti-Patterns Verdict

Partial trust. Core wallet surface is genuinely considered (three-tier card hierarchy; two-step money commit). Auth/consent shell (login, callback, terms-addendum-gate, gambling/activate) is interchangeable shadcn/AI-template output — the slop risk. Not slop overall.

## Overall Impression

Money model and safety UX are strong and trustworthy. Biggest opportunities: (1) R$ on credits flow contradicts the contract; (2) responsible-gambling promise (personal limits) with no UI; (3) legal surfaces ignore bilingual/i18n principle; (4) toast-only peak-end for real-money moves.

## What's Working

1. Three-tier visual hierarchy (balance-cards.tsx) encodes the design contract; subscription-only users see only the real card.
2. Error prevention (H5=4): fee/balance/million caps, max button, live fee preview, confirm step showing resulting balance, inline step-up MFA.
3. Quiet opt-in: one activateLink, never a banner; activate is a consent form.

## Priority Issues

[P1] R$ symbol on non-money credits flow — amount-dialog.tsx:165 hardcodes R$ for every flow incl. credits (sandbox). Sandbox must carry no currency symbol (contract + invariant #7). VERIFIED: magnitude is correct (formatCredits divides by 100 like formatBRL) — symbol/semantics bug only. Fix: conditional prefix; omit for credits.

[P1] Legal pages not internationalized — terms-addendum(/v1), gambling-addendum(/v1) page.tsx have entire bodies hardcoded pt; none via t(). Violates bilingual principle. legal-page.tsx:24 hardcodes "Última atualização:". Fix: move copy+headings into i18n catalogue.

[P1/P2] Personal-limit management UI absent — addenda center on daily/weekly/monthly limits; no entry point in any reviewed surface. Verify a limits route exists elsewhere; if not, trust/compliance gap. Fix: quiet limits entry on game card.

[P2] Stale v1 legal pages live + wrong — terms-addendum/v1 says sandbox "comprados com saldo real" + only "dois saldos"; contradicts three-wallet topology + invariant #7. Served/linkable, no noindex. Fix: redirect/remove or correct.

[P2] Placeholder contrast fails AA — globals.css:151 input::placeholder #94a3b8 on white = 2.57:1 (need 4.5). Fix: tokenize --placeholder ≥4.5:1.

[P2] No h1 on dashboard — dashboard uses span for logo, p/section for balance/ledger; no page heading. Fix: add h1.

[P2] Live status hidden <640px + color-only — dashboard/page.tsx:163-183 wraps WS status in hidden sm:inline-flex (mobile no feedback, leaves a11y tree); color dot only fails 1.4.1. Fix: textual state at all breakpoints.

[P2] Withdraw affordance too weak / hover contrast drop — balance-cards.tsx:70-77 bg-transparent text-white border-brand-400/60, lightens to bg-brand-500 on hover (white-on-violet < AA). Fix: visible neutral/secondary treatment, stable contrast.

[P2] Step-up MFA alert squished in button row — confirm-money-dialog.tsx:139-163 role=alert p is flex sibling of buttons in flex gap-2 (no wrap); aria-describedby points at generic copy. Fix: move alert above row.

[P2] Validation errors not announced — amount-dialog.tsx:216-220,241-245 errors are plain p, no role=alert/aria-live. Fix: add role=alert.

[P2] Money-move peak-end is a toast — after confirm, dialog closes, only sonner toast. Fix: brief success panel.

[P2] Generic auth/consent shell = slop — login/callback/terms-addendum-gate/gambling-activate share one template. Fix: unify brand mark + one considered motion.

[P3] Design-system drift — legal-page.tsx uses Tailwind default grays not tokens; error states use red-* not --destructive; globals.css:145 hardcodes input color/#fff (root cause of latent dark-mode invisible-text bug). Fix: route through tokens.

[P3] Latent dark-mode contrast — ledger-list.tsx:56 (brand-700 on dark card 2.53:1), balance-cards.tsx:96 (brand-600 3.14:1), amount-dialog.tsx:185 (black on transparent→dark card ~1:1) fail only if dark theme ships. No .dark toggle exists today. Fix: tokenize per-theme before enabling dark mode.

[P3] Minor — brand mark inconsistency (Image /app.svg vs lucide); decimal-entry trap (10.50→105 vs 10,50→1050, amount-dialog.tsx:177); next/image on local SVG (dashboard/page.tsx:155); hand-rolled traps vs modal primitive; logout 28px tap target.

## Persona Red Flags

- Sam (SR/keyboard): errors not announced; no h1; live status leaves a11y tree on mobile; traps lack inert.
- Jordan (first-timer): R$ on credits confusing; decimal trap; "Fund game" unexplained on real card.
- Casey (mobile): live status invisible <640px; 28–36px targets; backdrop discards amount.
- Brazilian PIX user: no saved PIX keys (CPF-mismatch risk); PIX dialog solid but no expiry time / persistent pending.
- Responsible-gambler opt-in: opt-in quiet + explicit (good), but no limit-setting UI despite promise.

## Questions to Consider

- Would dropping R$ on credits + showing "cr"/nothing make the money/credit boundary obvious at a glance?
- Should a withdrawal's peak-end be a quiet receipt rather than a vanishing toast?
- Is the generic auth shell intentional (backend minimalism) or should it carry more brand?
