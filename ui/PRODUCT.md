# Product

## Register

product

## Platform

web

## Users

Brazilian end users of the `aoctech.app` platform who hold money in the wallet: people paying subscriptions and services, depositing/withdrawing via PIX, and — only after explicit opt-in — playing skill games (poker/dominó) inside a ring-fenced pot. They are in a task every time they open the app: check a balance, move money, read a statement. Primary audience is the consumer; the surface is authenticated and personal. Secondary, surfaced only through the opt-in gambling flow, is the player persona — but it is the same person, never a separate screen identity.

## Product Purpose

A digital wallet that custodies real third-party money across three balances — `real` (PIX + subscriptions), `game` (real money ring-fenced for games, subject to the user's personal limits), and `sandbox` (virtual credits, no monetary value, never convertible). It exists so personal gambling limits have exactly one edge to meter (`real → game`), and so real money is never left in limbo. Success looks like: a user can see every balance at a glance, move money with unambiguous, auditable intent, and never encounter a surface that nudges them toward gambling or hides where their money is.

## Positioning

Three wallets, one audited edge: real money stays real money, and the only door into the gambling ring-fence is the one place your personal limit is actually enforced.

## Brand Personality

Sharp, efficient, confident. Terse, data-forward copy and motion that conveys state only. The interface behaves like a precise instrument, not a salesperson.

## Anti-references

- Legacy bank clutter: dense gray dashboards, tiny tables, jargon walls, decorative cruft.
- Crypto-bro flash: neon, volatility theater, speculative/casino energy, hype motion.
- Gamified casino patterns: banners, points, streaks, reward loops, or any nudge toward betting. The gambling addendum is opt-in and quiet by design — one link, never an upsell.

## Design Principles

1. **Clarity before delight.** A user managing real money must never pause at a subtly-off component. Familiar, correct affordances beat clever ones.
2. **Money has explicit semantics.** Color, weight, and fill encode what each balance *is* — real (filled), ring-fenced real (outlined), not-money (dashed, no currency). Never let the three read as interchangeable.
3. **Opt-in stays quiet.** The gambling surface appears only on consent, and the entry point is one quiet link — never a banner, upsell, or game-like reward.
4. **Motion is state, not decoration.** 150–250 ms; conveys loading, feedback, and transition only. No orchestrated page-load sequences, no decorative animation.
5. **Bilingual by default.** Portuguese (pt-BR) is primary, English is a first-class alternative. No string is hardcoded; every label goes through i18n.

The public homepage is statically pre-rendered under `/pt-BR` and `/en`. CloudFront redirects the legacy unprefixed URL using the `wallet_locale` preference cookie, falling back to the leading `Accept-Language` value. Each localized homepage owns its canonical URL, alternate-language links, description, and Open Graph locale. Authenticated and OAuth routes remain unprefixed so their registered callback and return-path contracts do not change; those routes resolve locale before rendering protected content. Legal documents are owned and hosted exclusively by CTech Accounts: wallet terms at `https://accounts.aoctech.app/products/wallet` and gaming terms at `https://accounts.aoctech.app/products/wallet-gaming`.

## Accessibility & Inclusion

WCAG 2.1 AA. Body text must clear 4.5:1 against its background; the violet brand ramp is the accent, not the text color on tinted surfaces. Reduced-motion users get instant or crossfade transitions. Inputs use explicit, readable placeholder contrast. All copy is i18n-keyed (pt-BR primary, en secondary) so the surface is never language-locked.
