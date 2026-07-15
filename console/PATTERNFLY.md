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

## What is loaded (post-W4 cutover)

PatternFly is the console's ONLY design system. The stylesheet stack, in load order, is:

1. `patternfly.css` - PF Core base + the per-component sheets we render (below).
2. `tokens.css` - the console's PF-native token layer (squares corners, system fonts, `--console-*` slots).
3. `console.css` - the shell rules (title bar, status-bar frame vars, tiling, launcher, layout).
4. `overrides.css` - the small ID/class-scoped escape hatch for PF-less shell chrome (status bar,
   floating gear, connection dot, keybindings grid, the refresh toast, the legacy `.ref-section` hide).
5. Per surface, lazily: `logs/logs.css`, `graph/graph.css`, `dashboard/dashboard.css`.

The Pico-era sheets - `pico.min.css`, `site.css`, `theme.css`, `ui-panels.css` - were **removed** at
the W4 cutover. Their few live dependents were migrated: the log filter chips (`.search-chips`/`.qchip`)
into `logs.css`, the refresh toast (`.sw-toast`) and the `.ref-section` hide into `overrides.css`, all
repointed to PF/`--console-*` tokens. The reference drawer (`ui/ref-drawer.ts`) is unused in the
decoupled console, so nothing else needed `ui-panels.css`. No `--pico-*` or `--c-*` reference remains
in `src`. **Remaining deferred optimization:** run PurgeCSS over the built bundle to drop the unused
PF token/component rules (the single biggest precache win) - tracked separately, intentionally NOT
part of W4.

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

## Bundle-size delta

At the W4 cutover the four Pico-era sheets were removed from `gen/`:

| Removed sheet (as shipped) | Bytes reclaimed |
| -------------------------- | --------------- |
| `pico.min.css` (raw copy)  | 83,319          |
| `site.css` (minified)      | 32,685          |
| `theme.css` (minified)     | 6,796           |
| `ui-panels.css` (minified) | 5,524           |
| **Total**                  | **~128,324 (~125KB)** |

Measured `gen/` total dropped **1,720KB -> 1,588KB (-132KB)** (the small extra beyond the
sheet bytes is the removed source no longer copied). A handful of migrated rules were added
back (`overrides.css` and `logs.css` grew a few hundred bytes each), so the net CSS reclaim
is ~125KB raw.

The dominant remaining CSS cost is `gen/patternfly.css` (~683KB minified) - the full
`--pf-t-*` token palette (light + dark) plus the imported component sheets. That is the
PurgeCSS target noted above: a fixed base cost independent of how few components we render,
and the single biggest remaining precache win once trimmed.
