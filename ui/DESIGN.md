---
name: CTech Wallet
description: A precise, restrained instrument for custoding real money across three balances.
colors:
  primary: "#7c3aed"
  neutral-bg: "#f8fafc"
  neutral-ink: "#0f172a"
  neutral-border: "#e2e8f0"
  neutral-muted: "#64748b"
  destructive: "#dc2626"
typography:
  display:
    fontFamily: "Geist, ui-sans-serif, system-ui, sans-serif"
    fontSize: "1.5rem"
    fontWeight: 600
    lineHeight: 1.2
    letterSpacing: "-0.01em"
  body:
    fontFamily: "Geist, ui-sans-serif, system-ui, sans-serif"
    fontSize: "0.875rem"
    fontWeight: 400
    lineHeight: 1.5
  label:
    fontFamily: "Geist, ui-sans-serif, system-ui, sans-serif"
    fontSize: "0.75rem"
    fontWeight: 600
    letterSpacing: "0.08em"
    textTransform: "uppercase"
rounded:
  sm: "0.375rem"
  md: "0.5rem"
  lg: "0.75rem"
  xl: "1rem"
  "2xl": "1.25rem"
spacing:
  sm: "8px"
  md: "16px"
  lg: "24px"
components:
  button-primary:
    backgroundColor: "{colors.primary}"
    textColor: "#ffffff"
    rounded: "{rounded.lg}"
    padding: "8px 10px"
    height: "32px"
  button-primary-hover:
    backgroundColor: "#6d28d9"
    textColor: "#ffffff"
    rounded: "{rounded.lg}"
    padding: "8px 10px"
    height: "32px"
  button-ghost:
    backgroundColor: "transparent"
    textColor: "{colors.neutral-ink}"
    rounded: "{rounded.lg}"
    padding: "8px 10px"
    height: "32px"
  button-outline:
    backgroundColor: "#ffffff"
    textColor: "{colors.primary}"
    rounded: "{rounded.lg}"
    padding: "7px 9px"
    height: "32px"
---

# Design System: CTech Wallet

## 1. Overview

**Creative North Star: "The Precise Instrument"**

CTech Wallet is a calibrated tool. Every screen reads like a precise meter — no flourish, just correct, legible state. Signal Violet is the signal light; cool slate gray is the casing. Nothing decorative fires; state is always legible. The brand personality is sharp, efficient, and confident: terse, data-forward copy and motion that conveys state only — the interface behaves like an instrument, not a salesperson.

This system explicitly rejects three things carried from PRODUCT.md: **legacy bank clutter** (dense gray dashboards, tiny tables, jargon walls), **crypto-bro flash** (neon, volatility theater, speculative energy, hype motion), and **gamified casino patterns** (banners, points, streaks, reward loops, any nudge toward betting). The gambling surface appears only on consent and the entry is one quiet link — never a lit banner.

**Key Characteristics:**
- Signal Violet is rare by design — one action color per screen, never a decorative tint.
- Surfaces are flat at rest; shadow answers elevation and state, not ambient styling.
- Money has explicit semantics: fill, stroke, and dash encode what each balance *is*.
- Bilingual by default — pt-BR primary, English first-class; every label goes through i18n.
- Motion is fast (150–250 ms) and state-only; it never decorates.

## 2. Colors: The Signal Violet Palette

A cool slate casing (never warm cream) carries the interface; one violet is the single signal. Violet is the action and the live state, gray is everything else.

### Primary
- **Signal Violet** (#7c3aed): the primary action, current selection, and live/active state. Used on ≤10% of any screen — its rarity is the point. On the filled real-money card it is the surface itself (white text on violet).
- **Signal Violet Soft** (#c4b5fd): secondary violet text on violet surfaces (e.g. the real card's eyebrow label), chosen for contrast against the filled card, not decoration.
- **Signal Violet Deep** (#6d28d9): the active/pressed and hover-deepen state of the primary action.

### Neutral
- **Casing** (#f8fafc): app canvas background behind white surfaces (the dashboard shell).
- **Surface** (#ffffff): cards, dialogs, inputs — the default container.
- **Ink** (#0f172a): primary text and headings; also the literal `input`/`textarea` text color.
- **Muted** (#64748b): secondary text, captions, hints. Never used for primary body copy on tinted surfaces.
- **Line** (#e2e8f0): borders, dividers, the ledger row separators.
- **Muted Line** (#cbd5e1): disabled/thin borders and scrollbar track.

### Named Rules
**The Signal Violet Rule.** Signal Violet is the only chromatic accent and it means exactly one thing: *this is the action, or this is live*. It must never become the dominant color of a screen, never tint a background decoratively, and never appear as more than one primary CTA per view. A second violet is a bug.

## 3. Typography

**Display Font:** Geist (with `ui-sans-serif, system-ui, sans-serif`)
**Body Font:** Geist (with `ui-sans-serif, system-ui, sans-serif`)
**Label/Mono Font:** Geist Mono (for all tabular numerics — money and ledger figures)

**Character:** One geometric sans carries the whole interface with quiet authority; the only contrast axis is Geist Mono stepping in wherever a number must align like a readout. No display/serif pairing, no personality font — the type disappears into the task.

### Hierarchy
- **Display** (600, 1.5rem / 24px, line-height 1.2, -0.01em): section and dialog titles (`text-lg font-semibold`).
- **Headline** (600, 1.125rem / 18px, line-height 1.3): card headings, prominent labels.
- **Title** (600, 1rem / 16px): list item titles, ledger row primary text.
- **Body** (400, 0.875rem / 14px, line-height 1.5): descriptions, hints, general copy. Held to 65–75ch where prose runs.
- **Label** (600, 0.75rem / 12px, 0.08em tracking, uppercase): eyebrows, wallet-type tags ("REAL", "GAME", "SANDBOX"), mono-tracked over violet.

### Named Rules
**The Mono-for-Money Rule.** Every numeric money value — balances, amounts, ledger figures — renders in Geist Mono with `tabular-nums`. Proportional numerals are forbidden in financial readouts; alignment is part of the instrument feel.

## 4. Elevation

Flat by default. A surface earns a shadow only when it lifts (card hover) or overlays (modal). Resting cards use a single whisper-soft shadow; the violet real-money card is filled, not shadowed. There is no ambient, room-filling shadow and no glass blur — depth is signal, not atmosphere.

### Shadow Vocabulary
- **card** (`0 1px 3px 0 rgb(0 0 0 / 0.07), 0 1px 2px -1px rgb(0 0 0 / 0.07)`): resting cards and the login panel.
- **card-hover** (`0 4px 12px 0 rgb(0 0 0 / 0.10), 0 2px 4px -1px rgb(0 0 0 / 0.06)`): the same card on hover, lifting it off the casing.
- **modal** (`0 20px 60px -10px rgb(0 0 0 / 0.25)`): dialog surfaces, isolating them from the 40%-gray scrim behind.

### Named Rules
**The Flat-By-Default Rule.** Surfaces are flat at rest. A shadow is a response to state (hover, elevation, focus-ring adjacent) — never ambient decoration. Glassmorphism and heavy drop shadows are prohibited.

## 5. Components

### Buttons
- **Shape:** gently rounded (10px radius, `rounded-lg`), height 32px, text-sm, medium weight.
- **Primary (brand):** Signal Violet fill, white label. The single CTA per screen. Hover deepens to #6d28d9.
- **Outline:** white surface, 1px Signal Violet border at 60% (`border-brand-400/60`), violet label — used for *secondary* money actions on the violet card (withdraw, fund-game) and for neutral secondary actions elsewhere (return, credits). Hover tints the fill.
- **Ghost:** transparent, ink label; for low-emphasis controls (logout icon, dialog cancel). Hover fills a muted gray.
- **Destructive:** red-tinted text/background for irreversible confirmations; red-600 fill where a hard delete is offered.
- **States:** every variant carries default, hover, focus-visible (3px ring), active (1px press), disabled (50% opacity, no pointer). Loading maps to the disabled state with a pending flag.

### Inputs / Fields
- **Style:** white fill, 1px Line border, 10px radius, 40px height, text-sm ink text, readable muted placeholder (#94a3b8 meets 4.5:1).
- **Focus:** `focus-within` / `focus` shifts the border to Signal Violet and raises a 3px violet ring at 20% (`ring-brand-500/20`).
- **Error:** border turns red-400, ring red-500/20, and a red-600 message sits below the field. aria-invalid is set; the message is `aria-describedby`-linked.
- **Money input:** the amount field prefixes a muted "R$" and renders the typed value in Geist Mono `tabular-nums`; a max-length guard caps entry at the R$ 1.000.000 ceiling on deposits and game funding.

### Cards / Containers
- **Corner Style:** 16px radius (`rounded-2xl`) on the balance cards; 12px (`rounded-xl`) on the ledger panel and dialogs.
- **Background:** white for neutral containers; the real-money card is Signal Violet filled.
- **Border:** 1px Line on neutral cards; the game card uses a 2px Signal Violet border (`border-brand-200`) to read as ring-fenced; the sandbox card uses a 1px dashed Line.
- **Shadow Strategy:** see Elevation — resting `card`, hover `card-hover`.
- **Internal Padding:** 24px (balance cards), 20px (ledger panel), 24px (dialog).

### Navigation
- **Style:** a single top bar, white with a 1px bottom Line, max-width 4xl, 24px vertical padding. The brand mark is a violet rounded square (violet-600 fill, white glyph) beside "CTech Wallet".
- **States:** the user name (hidden under sm) and a ghost logout icon on the right. No tab rail on the dashboard; the ledger switches via in-panel text tabs, not nav.

### [Signature Component] Balance Hierarchy
The three balances are deliberately **not** a symmetric card set — the visual treatment encodes what each balance is, so they cannot be mistaken for one another:
- **Real — money:** solid filled Signal Violet card, white text, R$ prefix, bold mono balance, primary Deposit/Withdraw actions.
- **Game — real money, ring-fenced:** outlined card (2px Signal Violet border, white fill), R$ prefix, violet "GAME" tag. Spendable only on games, subject to the user's personal limit.
- **Sandbox — not money:** dashed-border card, **no currency symbol**, explicit "não vira dinheiro" line; it has no monetary value and can never convert back.
- **Not activated:** the gambling surface is absent; one quiet link ("activate") stands in, never a banner or upsell.

### [Signature Component] Ledger Row
A divided list (`divide-gray-100`) of entries: left side is the localized type label (title weight, ink) over a muted timestamp; right side is the signed amount in Geist Mono — violet (#6d28d9 / brand-700) when inflow, muted gray when outflow. Loading and empty states are explicit, centered, and teach the interface ("nothing here yet") rather than spinner-in-content.

## 6. Do's and Don'ts

### Do:
- **Do** reserve Signal Violet for the single primary action and live state per screen (The Signal Violet Rule).
- **Do** render the three balances with explicit semantics — filled = real, outlined = ring-fenced real, dashed = not money (The Money-Has-Semantics Rule).
- **Do** keep surfaces flat; let shadow answer hover and elevation only (The Flat-By-Default Rule).
- **Do** render every money value in Geist Mono `tabular-nums` (The Mono-for-Money Rule).
- **Do** keep transitions at 150–250 ms and state-only; honor `prefers-reduced-motion` with a crossfade or instant cut.
- **Do** write all copy through i18n, pt-BR primary and English first-class.
- **Do** hold body text to ≥4.5:1 and use a readable muted placeholder (#94a3b8), never light gray-on-tint.

### Don't:
- **Don't** use border-left/right greater than 1px as a colored stripe on any card, list item, or alert.
- **Don't** use gradient text (`background-clip: text` with a gradient) — emphasis is weight or size, one solid color.
- **Don't** use glassmorphism as default; blurs and glass cards are decorative and prohibited.
- **Don't** build identical icon-plus-heading-plus-text card grids as the page's rhythm.
- **Don't** put a tiny uppercase tracked eyebrow above every section, or 01/02/03 numbered markers as default scaffolding.
- **Don't** animate decorative motion, orchestrate page-load sequences, or bounce/elastic.
- **Don't** invent affordances — standard buttons, dialogs, and form controls only; modals are the last resort, not the first thought.
- **Don't** let any heading overflow its container at tablet/mobile widths.
- **Don't** treat the three wallets as interchangeable: never show a currency symbol on sandbox, never fill the game card, never dash the real card.
- **Don't** drift into **legacy bank clutter** — no dense gray dashboards, tiny tables, or jargon walls.
- **Don't** drift into **crypto-bro flash** — no neon, no volatility theater, no speculative/casino energy, no hype motion.
- **Don't** drift into **gamified casino patterns** — no banners, points, streaks, reward loops, or any nudge toward betting; the gambling entry stays one quiet link.
