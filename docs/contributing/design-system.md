# Design system

All of Repowire's surfaces — the marketing site, these docs, and the hosted relay dashboard — use the **Repowire Design System**: light-first, warm-paper neutrals, a single cobalt accent, and Inter for body and prose. It reads closer to Linear, Resend, or Stripe documentation than to a terminal.

The earlier dark, mono-forward "Copper Mesh" direction is fully retired. The dashboard keeps an operator/terminal character within the new system by using JetBrains Mono for its technical chrome (labels, section headings, peer names, paths, IDs, timestamps, the live feed) while everything else stays Inter — see [Dashboard](#dashboard).

## Voice

- Write builder-to-builder copy: calm, specific, short technical sentences over marketing language.
- Use second person for the reader (you) and third person for the product (Repowire).
- Use sentence case everywhere: headings, buttons, nav, eyebrows. Reserve all caps with wide tracking for short eyebrow labels (`OVERVIEW`) and status badges (`BETA`, `NEW`), never paragraphs or headlines.
- Use mono for code, identifiers, paths, commands, and inline literals: `ask`, `notify_peer`, `port 8377`. Never for headings.
- No emoji in product chrome. User-authored content can contain anything the user wrote.
- No exclamation marks, no superlatives. Prefer "Local by default" to "Blazing fast".

Common patterns:

| Pattern | Example |
| --- | --- |
| Inline command | `repowire up` |
| Route in prose | `dashboard → backend` (UTF-8 arrow) |
| Peer reference | `@backend` (mono, no special color) |
| Status label | "Online" (sentence case, paired with a status dot) |

## Color

Light-first warm paper with a single cobalt accent. The bulk of the surface is neutral; cobalt is for links, primary CTAs, and focus rings only. Status colors are not brand colors.

| Role | Token | Light | Dark |
| --- | --- | --- | --- |
| Page | `--page` | `#fcfbfa` | `#0e0e0d` |
| Surface | `--surface` | `#ffffff` | `#161614` |
| Sunken | `--surface-sunken` | `#f7f6f3` | `#0a0a09` |
| Primary text | `--fg` | `#141413` | `#f3f1ec` |
| Muted text | `--fg-muted` | `#4e4b46` | `#b5b1a8` |
| Accent | `--accent` | `#2c54dd` (cobalt-500) | `#5778e6` (cobalt-400) |
| Accent surface | `--accent-soft` | `#eef2ff` | `rgba(87,120,230,0.16)` |
| Success / warning / danger / info | `--success` … | green / amber / red / blue | brighter equivalents |

Rules:

- The page is warm paper, not pure white. White is reserved for cards so they sit slightly off the page.
- One accent only. Don't introduce a second brand hue.
- Dark mode is warm-dark (`#0e0e0d`), not pure black. Borders read at 8% white.
- CTAs are solid cobalt, no gradients.
- Reach for type, scale, and space before reaching for color.

## Type

Inter for everything; JetBrains Mono only for code.

| Family | Usage |
| --- | --- |
| Inter (400/500/600/700) | UI, headings, body, labels, nav |
| JetBrains Mono (400/500/700) | Code blocks, inline code, identifiers, paths, commands |

In the marketing app, Inter loads via `next/font/google` and JetBrains Mono via `next/font/local`. The docs site loads Inter through mkdocs-material's `font.text` setting.

Scale (responsive via `clamp()` where it matters): `--fs-display` 40–64px (hero only), `--fs-h1` 32–44px, `--fs-h2` 24–32px, down to `--fs-body` 16px and `--fs-eyebrow` 12px. Tabular numerals on counts and timestamps; tight tracking on display and headings; body line-height 1.55.

## Space, radii, motion

- 4px grid. Marketing sections are airy (64–96px vertical). Container width is 1200px (`--container-xl`).
- Radii default to 10px (`--rds-radius`); cards 10–14px; large media 20px. Pills (`999px`) only for status badges and toggles.
- Borders are a single hairline, `1px solid var(--rds-border)`.
- Shadows are a soft family (`--shadow-xs`…`--shadow-xl`). Cards rest flat or at `--shadow-xs` and bump one tier on hover.
- Motion: `--rds-ease` `cubic-bezier(0.32, 0.72, 0, 1)`, 120ms for color/opacity, 200ms for layout. A soft `translateY(-1px)` card-hover lift and the online status-dot pulse are allowed. No scale on cards, no spring easings, no scroll-triggered staggers.
- Focus: 2px cobalt ring with a 2px page offset (`--ring`).

## Theming

- Light-first. The marketing site defaults to light and offers a dark toggle in the top bar; the choice persists to `localStorage["repowire-theme"]` and falls back to the OS `prefers-color-scheme`. A small inline script in `web/app/layout.tsx` sets `data-theme` before first paint to avoid a flash.
- The active theme is selected by `data-theme="light"` / `data-theme="dark"` on `<html>`. RDS semantic tokens are scoped to those attributes.
- The docs site uses mkdocs-material's built-in light/dark palette toggle, themed in `docs/stylesheets/repowire-cobalt.css`.

## Iconography

Lucide, 1.5–1.75px stroke. In the marketing app, use `lucide-react`. Brand glyphs Lucide no longer ships (e.g. GitHub) are inlined as small SVG components. The brand mark is `logo-mark.svg`. No emoji and no unicode glyphs as UI icons; real arrows in prose (`dashboard → backend`) are fine.

## Assets

Production web assets live under `web/public/brand/`; docs assets under `docs/assets/`.

- `logo-mark.svg`: primary brand mark (used in marketing nav/footer and the docs logo).
- `repowire-arch.webp`: architecture/product visual.

The generated `repowire-design-system/` handoff bundle is a reference, not a production dependency.

## Implementation notes

- The RDS tokens live in `web/app/globals.css` as an additive layer: a neutral/cobalt raw palette plus `data-theme`-scoped semantic tokens (`--page`, `--surface`, `--fg`, `--accent`, `--rds-border`, `--shadow-*`, `--ring`).
- Marketing component styles are scoped under `.rw-marketing` in `web/app/marketing.css` so they never leak into the dashboard, which shares `globals.css`.
- Font tokens `--rds-font-sans` / `--rds-font-mono` are defined on `.rw-marketing` (not `:root`) because they reference the `next/font` variables set on `<body>`.
- Prefer `lucide-react` for new icons. Avoid one-off colors; if a color repeats, promote it to a token.
- The docs stylesheet mirrors these tokens by hand; keep `docs/stylesheets/repowire-cobalt.css` in sync with `globals.css`.

## Dashboard

The hosted relay dashboard (`web/app/dashboard/`) is on the Repowire Design System: cobalt accent, warm-paper light surfaces, light-first with a dark counterpart, tight radii for density. It inherits `data-theme` from the root layout, so it follows the same theme toggle as the marketing site.

It keeps an operator/terminal character through typography: JetBrains Mono for the technical chrome — the `REPOWIRE`/`DASH` wordmark, uppercase annunciator labels (count pills, connection badge, view tabs), section headings, peer names, paths, IDs, timestamps, and the live feed. Inter is used for body text and chat/markdown prose. Status semantics stay constant: online → success (green), busy → warning (amber), offline → muted, error → danger (red), active → cobalt.

The dashboard reaches these via Tailwind's `@theme` color tokens in `web/app/globals.css`, which are repointed to the RDS palette. Because Tailwind v4 emits non-alpha utilities as `var(--color-*)` (flipped by a `[data-theme="dark"]` re-declaration) but bakes opacity-modifier colors at build time, the color tokens use concrete light hex with a dark override, while the font and radius tokens use a separate `@theme inline` block so they resolve the `next/font` variables set on `<body>`.
