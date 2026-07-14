---
target: ctech-wallet-ui all surfaces
total_score: 26
p0_count: 1
p1_count: 3
timestamp: 2026-07-14T22-47-31Z
slug: ctech-wallet-ui-src
---
# Critique: CTech Wallet UI (all surfaces)

## Design Health Score
Total: 26/40 — Good band, real foundation, fix weak areas before release.

| # | Heuristic | Score | Key Issue |
|---|-----------|-------|-----------|
| 1 | Visibility of System Status | 3 | Skeletons/toasts/polling exist; `processing` withdrawals (invariant #12) have no persistent status surface. |
| 2 | Match System / Real World | 3 | pt-BR, plain copy, clear money semantics. Solid. |
| 3 | User Control and Freedom | 2 | Dialogs have Cancel but no Esc-to-close, no focus trap, no click-outside. |
| 4 | Consistency and Standards | 3 | Mostly consistent; mixed token systems + two gradient mechanisms + v3/v4 config split. |
| 5 | Error Prevention | 3 | Zod caps + idempotency strong; irreversible real-money moves have no confirm + fees hidden. |
| 6 | Recognition not Recall | 3 | Icons+labels, translated ledger types. Fine. |
| 7 | Flexibility and Efficiency | 2 | No quick-amount presets, no "Max" button despite known maxCents, no keyboard shortcuts. |
| 8 | Aesthetic and Minimalist | 3 | Clean; saved by money semantics, undercut by eyebrow chrome + dead violet wash. |
| 9 | Help and Documentation | 2 | "Closing is safe" copy good; 2% withdrawal fee invisible; no legend for two R$ balances. |
| 10 | Error Recovery | 2 | RFC 7807→i18n mapping excellent; ledger error has no retry; toasts can vanish. |

## Anti-Patterns Verdict
NOT slop. The non-symmetric balance-card hierarchy (filled=real, outlined=ring-fenced, dashed=not-money) is genuinely earned anti-slop and maps 1:1 to the financial-safety invariants. No side-stripe, gradient text, glass, or card-grid tells.
Two chrome tells: (1) uppercase tracked mono eyebrows on every balance label + ledger tabs (contradicts "terse, data-forward"); (2) a global violet wash in layout.tsx painted over by the dashboard's opaque bg-gray-50 — dead layer.
Deterministic scan: 2 advisory findings, both rule-true/impact-false (3px scrollbar radius; 0.8rem undocumented sm-button step). Real finding from scan: 71 raw gray-* usages across 15 files vs ~9 semantic-token usages (bg-card 0, text-muted-foreground 0) — dark mode effectively unthemed on primary surfaces.

## What's Working
1. balance-cards.tsx money semantics — distinctive, invariant-aligned.
2. Quiet opt-in — gambling entry is one Link, never a banner.
3. dashboard problemMessage — RFC 7807 → i18n bridge done right.

## Priority Issues
- [P0] Withdrawal / fund-game / return-game: no confirm step, 2% fee never shown. Real money leaves on one click. Fix: review/confirm screen (amount, PIX key, fee, resulting balance). → /impeccable harden
- [P1] Contrast: sandbox label+icon text-gray-400 ~2.5:1 (fail AA); game eyebrow text-brand-500 ~4.2:1; real eyebrow text-brand-200 ~4.1:1 on violet. Fix: sandbox→gray-500+, game eyebrow→brand-600/700, real eyebrow→brand-100. → /impeccable audit
- [P1] Two R$ balances lack in-app explanation; first-timer can misread game as directly withdrawable. Fix: one-line explainer / ? on game card. → /impeccable clarify
- [P1] Tailwind v3/v4 mismatch: bg-gradient-login / shadow-card / shadow-modal defined in tailwind.config.ts (v3) while globals.css is v4 @import. If v4 ignores JS config, auth/activate/gate screens render with no gradient/shadow. Must verify build. → /impeccable audit
- [P2] Dialogs (amount, pix-charge) not accessible modals: no role="dialog"/aria-modal/aria-labelledby, no Esc, no focus trap; PixChargeDialog no autofocus. → /impeccable polish
- [P2] Gray-token bypass breaks dark mode; migrate to semantic tokens (bg-card/border-border/text-muted-foreground). → /impeccable audit
- [P3] reduced-motion: 0 handlers despite mandate; add global motion-reduce fallback. → /impeccable audit
- [P3] Eyebrow chrome + dead violet wash + amount-dialog starts value="0,00". → /impeccable quieter / typeset

## Persona Red Flags
- Sam (a11y): gray-400 labels ~2.5:1 fail AA + 3:1 icon rule; dialogs keyboard-trapped-unsafe (no Esc, no trap).
- Jordan (first-timer): two R$ balances, no legend; withdrawal hides 2% fee, no confirm → "where did the fee go?" shock.
- Casey (mobile): real card 3 buttons wrap on 360px dropping 3rd below fold; amount-dialog opens "0,00" invites 0-submit.

## Minor Observations
Dead brand wash (layout violet painted over by dashboard bg-gray-50). amount-dialog hardcodes R$ prefix even for credits (sandbox rule says no symbol). not-found reuses raw gray. PixChargeDialog copy button good a11y pattern.

## Questions
1. Two R$ balances for every activated user — is filled/outlined cue strong enough, or does a second withdrawable-looking R$ invite "why can't I pull this out?"?
2. 2% withdrawal fee entirely absent from UI — transparency gap or deliberate?
3. processing withdrawals have only toasts, no persistent pending state — appropriate for custodied money?
