# PatternFly in the console (W0 spike reference)

This is the reference the bulk PatternFly migration (W1-W4) copies. It records the
pinned version, the token map, the class-vs-ID convention, and the measured bundle
delta. See the plan for the full workstream sequence: `patternfly-migration-plan.md`.

## Pinned version

`@patternfly/patternfly@6.5.2` (devDependency, exact pin). Published 2026-05-21, so it
clears `.npmrc`'s `minimum-release-age=14400` (10 days) supply-chain gate with a wide
margin. PatternFly Core (CSS only, no JS runtime) - the documented path for non-React
consumers. Prefix `pf-v6`; expect a `pf-v6 -> pf-v7` churn at the next major, contained
to the class strings and this token file.

## How PatternFly is bundled

- `src/styles/patternfly.css` @imports the PF **base** + only the **per-component** sheets
  we render (Button, Tabs, Card, Gallery). esbuild `--bundle --minify` inlines them into
  `gen/patternfly.css`. Add a component's sheet here when a surface starts using it - that
  is the whole opt-in surface. Do NOT import the 1.8MB monolith `patternfly.min.css`.
- Font/image `url()` assets are marked `--external` in the build script so esbuild leaves
  the urls instead of trying to inline PF's ~10MB `assets/`. All such urls live in
  `patternfly-base.css` (RedHat/FontAwesome/pficon webfonts, PF background SVGs) and are
  **token default values**, not referenced by the Tabs/Card/Button/Gallery markup we emit.
  `tokens.css` overrides the RedHat body/heading/mono font tokens to a system stack, so
  those @font-face rules are never referenced and never fetched. Net: no 404s for the
  rendered title bar or card. If a later surface renders pficon glyphs or a masthead/page
  background, W1 must ship a trimmed `assets/` subset (or override those tokens too).

## Token map (`src/styles/tokens.css`)

The ONE file adapting PF tokens to the console. Fresh - NOT a port of the old `--c-*` /
`--pico-*` palette.

| Console slot                | PatternFly token                                        | Meaning              |
| --------------------------- | ------------------------------------------------------- | -------------------- |
| `--console-accent`          | `--pf-t--global--color--brand--default`                 | primary/active accent |
| `--console-status-running`  | `--pf-t--global--icon--color--status--info--default`    | pool busy (blue)     |
| `--console-status-queued`   | `--pf-t--global--icon--color--status--danger--default`  | saturation (red)     |
| `--console-status-ok`       | `--pf-t--global--icon--color--status--success--default` | healthy (green)      |
| `--console-status-warn`     | `--pf-t--global--icon--color--status--warning--default` | caution (gold)       |

These PF status tokens are **theme-aware** (they resolve to the right value in light and
dark), so charts that read `--console-status-*` at runtime via `getComputedStyle` color
correctly in both themes with no per-theme code. Charts (uplot.ts, tiles) repoint here in
W3; the utilization tile already does (`--console-status-running` / `--console-status-queued`).

### Corner style (squarish, locked house style)

PF builds every radius from global primitives `--pf-t--global--border--radius--{100..500}`
plus semantic/role aliases. `tokens.css` overrides the primitives AND the aliases (small/
medium/large/tiny/pill + action/control roles) to **2px**, once, so the whole component set
(cards, buttons, inputs, tabs, chips) squares up together - no per-component CSS, survives
version bumps.

## Class-vs-ID convention (enforce in review; linter deferred)

- **`pf-v6-*` classes are the ONLY class vocabulary.** Consume PF component/layout/utility
  classes as-is; do not re-skin, alias, or invent custom presentational classes.
- **App hooks are IDs and `data-*`, never new classes** (`id="console-tabs"`,
  `data-tab-id`, `data-card`).
- **Accessibility is semantic elements + ARIA**, orthogonal to the classes: keep
  `<header>/<main>/<footer>` landmarks, real `<button>`, `role`/`aria-*`.
- **Escape hatch:** one small audited `overrides.css` for a genuinely PF-less bit (canvas/
  graph host sizing). Prefer a `pf-v6-u-*` utility or an ID-scoped rule first.

## Bundle-size delta (measured, W0)

Additive numbers (PF added ALONGSIDE the Pico-era sheets; W4 removes Pico).

| Metric                     | Before     | After (with PF) | Delta        |
| -------------------------- | ---------- | --------------- | ------------ |
| `gen/` total (all files)   | 1,007,224  | 1,473,906       | +466,682     |
| All CSS, raw               | 197,659    | 662,345         | +464,686     |
| All CSS, gzipped           | 31,739     | 79,533          | +47,794      |
| `gen/patternfly.css` alone | -          | 464,861 (48KB gz) | new         |

The dominant cost is `patternfly-base.css`: **~321KB minified on its own** (the full
`--pf-t-*` token palette, light + dark). It is a fixed cost independent of how many
components are imported; per-component sheets are small on top of it. At W4 cutover the
Pico-era sheets drop (pico.min.css 83KB, site.css 33KB, ui-panels.css 6KB, theme.css 7KB,
most of console.css) which claws back ~145KB raw, but PF base keeps the raw precache well
above the Pico baseline. Over the wire it is modest (+48KB gzipped). **Follow-up for the
bulk migration:** run PurgeCSS/`@fullhuman` over the built bundle to drop unused token
definitions and component rules - the single biggest precache win, and the plan's stated
"trim unused, don't ship silently" gate.
