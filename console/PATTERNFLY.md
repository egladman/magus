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

## Custom-CSS naming methodology (STRICT - the formula for every class we author)

PatternFly owns the `pf-v6-*` vocabulary; we consume it as-is and invent NOTHING that
overlaps it. But some bits have no PF component (the status bar, the ANSI log body, the
graph stage, the gantt, the keybinding table, ...) and we must author classes for them.
Every such class MUST follow the formula below - as disciplined, prefixed, and greppable
as PatternFly's own names - so the custom surface stays tiny, self-documenting, collision-
proof, and mechanically maintainable. There are NO bare, ad-hoc, or unprefixed class names.
This mirrors PF's `pf-v6-c-<block>__<element>` + `pf-m-<modifier>` BEM structure.

### The formula

    console-<area>-<block>[__<element>][--<modifier>]

- **`console-`** - the app namespace (parallel to `pf-v6-`). EVERY custom class starts with
  it. A bare class like `.badge` or `.qchip` is forbidden; `grep -r "class=" | grep -v "pf-v6-\|console-"`
  must eventually return nothing but real HTML attributes.
- **`<area>`** - the region/surface that OWNS the class (parallel to PF's `c`/`l`/`u` slot).
  The allowed areas are a CLOSED set - pick exactly one:
  - `console-shell-*`     the app frame: title bar, tab strip, status bar, floating gear +
                          settings popover, command palette, keybindings overlay, tiling.
  - `console-dashboard-*` the dashboard surface (hero, tiles, gantt, pool, stat strips, tables).
  - `console-log-*`       the log viewer surface (filter chips, toolbar bits, zoom control).
  - `console-graph-*`     the graph explorer surface (stage, sidebar, node cloud, legend, explain card).
  - `console-activity-*`  the activity surface (only what is not already shared render).
  - `console-render-*`    the SHARED render model reused by log + activity (foldable sections,
                          status badges, ANSI spans) - one home so both surfaces stay in lockstep.
- **`<block>`** - the component/thing, kebab-case, verbose and explicit. Prefer a full word to
  an abbreviation: `console-log-filter`, `console-shell-statusbar`, `console-dashboard-gantt`,
  `console-graph-nodelist`, `console-render-badge`, `console-render-ansi`.
- **`__<element>`** - a PART of the block (BEM double-underscore): `console-shell-statusbar__dot`,
  `console-log-filter__chip`, `console-dashboard-gantt__bar`, `console-graph-nodelist__pill`.
  Elements do NOT nest in the name (never `__row__cell`); flatten to `__cell` under the block.
- **`--<modifier>`** - a fixed structural/categorical VARIANT (BEM double-hyphen), used ONLY for a
  closed enumerated set: `console-render-ansi__fg--red`, `console-render-badge--pass`,
  `console-dashboard-gantt__bar--failed`. Do NOT use `--modifier` for transient STATE.

### State is `data-*`, not a class (keeps the closed convention closed)

Transient/boolean state (active, collapsed, focused, capturing, hidden, selected, a live/health
value) is a `data-*` attribute on the element, styled as an attribute selector - NEVER a
`--modifier` class. This matches the existing app-hook convention (`data-state`, `data-health`,
`data-collapsed`, `data-focus`). So: `console-shell-statusbar__dot[data-state="connected"]`,
`console-dashboard-tile[data-collapsed]`. Reserve `--modifier` for the fixed vocabularies where an
enumerated class reads better (the 6 ANSI colours, the badge kinds, the gantt bar kinds).

### IDs, data-* hooks, and PF classes are already fine - do not rename them

`#console-titlebar`, `#console-statusbar`, `#console-tabs`, `#console-outlet`, `data-tab-id`,
`data-pane-id`, `data-open`, `data-card`, every `pf-v6-*` - all stay. The formula governs only the
custom CSS CLASSES we author. A JS "hook" that carries no styling should be a `data-*` attribute,
not a class, wherever practical.

### Examples (legacy ad-hoc -> the convention)

    .a-fg-red        -> .console-render-ansi__fg--red
    .a-bold          -> .console-render-ansi--bold
    .badge-pass      -> .console-render-badge--pass
    .log-section     -> .console-render-section
    .file-bar        -> (dead: logs is a pf-v6-c-toolbar now - delete, do not rename)
    .status-item     -> .console-shell-statusbar__item
    .conn (dot)      -> .console-shell-statusbar__dot   (+ [data-state]/[data-health])
    .dash-hero       -> .console-dashboard-hero
    .gantt-bar       -> .console-dashboard-gantt__bar   (+ --running/--failed/... variants)
    .node-pill       -> .console-graph-nodelist__pill
    .k-<kind> dot    -> .console-graph-legend__swatch   (+ data-kind="<kind>")
    .sw-toast        -> .console-shell-toast
    .qchip           -> .console-log-filter__chip

### The pending rename (the ~323 legacy classes)

The migration KEPT the console's original ad-hoc class vocabulary for the escape-hatch content
PF cannot render (ANSI body, gantt, node cloud, badges, ...) - repointing colours but not renaming.
An audit counts ~323 distinct non-`pf-v6-`/non-`console-` classes still in
console.css / overrides.css / logs.css / graph.css / dashboard.css and the TS that emits them
(`grep -rhoE '\.[a-zA-Z][a-zA-Z0-9_-]+' src/**/*.css | sort -u | grep -vE '^\.(pf-|console-|js$|no-js$)'`).
Renaming them to this formula (in the CSS AND the class strings in the .ts builders + scaffold.html,
in lockstep so nothing breaks) is a dedicated follow-up pass - do it area by area
(render/shell/log/graph/dashboard/activity), rebuild + browser-verify each, keep typecheck/tests green.
From this point ON, no NEW custom class may be written except in this formula.
