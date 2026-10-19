# Product

## Register

product

## Platform

web

## Users

Brazilian end users of the `aoctech.app` platform who hold money in the wallet: people paying subscriptions and services (Pix deposit and withdraw, handled by `ctech-billing`), and who stake that money in skill games (poker/dominó) played in Poker, which owns consent, limits and self-exclusion. They are in a task every time they open the app: check a balance, move money, read a statement. The audience is the consumer; the surface is authenticated and personal.

## Product Purpose

A digital wallet that custodies real third-party money across two balances — `real` (Pix, subscriptions and game stakes) and `sandbox` (virtual credits, no monetary value, never convertible; bought and spent in Poker). It exists so real money is never left in limbo. Success looks like: a user can see every balance at a glance, move money with unambiguous, auditable intent, and never encounter a surface that nudges them toward gambling or hides where their money is.

## Positioning

Two balances, one audited ledger: real money stays real money, and virtual credits never become money.

## Brand Personality

Sharp, efficient, confident. Terse, data-forward copy and motion that conveys state only. The interface behaves like a precise instrument, not a salesperson.

## Anti-references

- Legacy bank clutter: dense gray dashboards, tiny tables, jargon walls, decorative cruft.
- Crypto-bro flash: neon, volatility theater, speculative/casino energy, hype motion.
- Gamified casino patterns: banners, points, streaks, reward loops, or any nudge toward betting. The wallet has no gambling surface; never add one.

## Design Principles

1. **Clarity before delight.** A user managing real money must never pause at a subtly-off component. Familiar, correct affordances beat clever ones.
2. **Money has explicit semantics.** Color, weight, and fill encode what each balance *is* — real (filled), not-money (dashed, no currency). Never let them read as interchangeable.
3. **Motion is state, not decoration.** 150–250 ms; conveys loading, feedback, and transition only. No orchestrated page-load sequences, no decorative animation.
4. **Bilingual by default.** Portuguese (pt-BR) is primary, English is a first-class alternative. No string is hardcoded; every label goes through i18n.

The public homepage is statically pre-rendered under `/pt-BR` and `/en`. `/` is a plain exported page that prerenders pt-BR and switches client-side from the stored preference or `Accept-Language` (no edge redirect). Each localized homepage owns its canonical URL, alternate-language links, description, and Open Graph locale. Authenticated and OAuth routes remain unprefixed so their registered callback and return-path contracts do not change; those routes resolve locale before rendering protected content. Legal documents are owned and hosted exclusively by CTech Accounts: wallet terms at `https://accounts.aoctech.app/products/wallet` and gambling terms at `https://accounts.aoctech.app/products/wallet-gaming`.

## Accessibility & Inclusion

WCAG 2.1 AA. Body text must clear 4.5:1 against its background; the violet brand ramp is the accent, not the text color on tinted surfaces. Reduced-motion users get instant or crossfade transitions. Inputs use explicit, readable placeholder contrast. All copy is i18n-keyed (pt-BR primary, en secondary) so the surface is never language-locked.
