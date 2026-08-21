# The console

The native console PWA: a standalone pnpm project, built and served independently of the
docs site. This file is the conventions the rest of the console's source cites by name -
the stylesheet stack, the token map, and the naming rules every authored class follows.

## PatternFly

`@patternfly/patternfly@6.5.2` (devDependency, exact pin). PatternFly Core - CSS only, no
JS runtime - which is the documented path for non-React consumers. Prefix `pf-v6`; expect a
`pf-v6 -> pf-v7` churn at the next major, contained to the class strings and `tokens.css`.

PatternFly is the console's ONLY design system. The stylesheet stack, in load order:

1. `patternfly.css` - PF Core base + the per-component sheets we render.
2. `tokens.css` - the console's PF-native token layer (squares corners, system fonts, `--console-*` slots).
3. `console.css` - the shell rules (title bar, navigation rail, status-bar frame vars, tiling,
   launcher, layout).
4. `overrides.css` - the small ID/class-scoped escape hatch for PF-less shell chrome.
5. Per surface, lazily: `logs/logs.css`, `graph/graph.css`, `dashboard/dashboard.css`.

### How it is bundled

- `src/styles/patternfly.css` @imports the PF **base** plus only the **per-component** sheets
  we render (Button, Tabs, Card, Gallery). esbuild `--bundle --minify` inlines them into
  `gen/patternfly.css`. Add a component's sheet here when a surface starts using it - that is
  the whole opt-in surface. Do NOT import the 1.8MB monolith `patternfly.min.css`.
- Font/image `url()` assets are marked `--external` in the build script so esbuild leaves the
  urls instead of inlining PF's ~10MB `assets/`. All such urls live in `patternfly-base.css`
  and are **token default values**, not referenced by the markup we emit; `tokens.css`
  overrides the RedHat body/heading/mono font tokens to a system stack, so those `@font-face`
  rules are never referenced and never fetched. If a later surface renders pficon glyphs or a
  masthead background, it must ship a trimmed `assets/` subset or override those tokens too.
- `gen/patternfly.css` (~683KB minified) is the dominant CSS cost - the full `--pf-t-*` palette
  for both themes plus the imported component sheets. It is a fixed base cost independent of
  how few components we render. Running PurgeCSS over the built bundle is the single biggest
  remaining precache win, and is deliberately not done yet.

## Token map (`src/styles/tokens.css`)

The ONE file adapting PF tokens to the console.

| Console slot               | PatternFly token                                        | Meaning               |
| -------------------------- | ------------------------------------------------------- | --------------------- |
| `--console-accent`         | `--pf-t--global--color--brand--default`                 | primary/active accent |
| `--console-status-running` | `--pf-t--global--icon--color--status--info--default`    | pool busy (blue)      |
| `--console-status-queued`  | `--pf-t--global--icon--color--status--danger--default`  | saturation (red)      |
| `--console-status-ok`      | `--pf-t--global--icon--color--status--success--default` | healthy (green)       |
| `--console-status-warn`    | `--pf-t--global--icon--color--status--warning--default` | caution (gold)        |

These PF status tokens are **theme-aware** - they resolve to the right value in light and
dark - so charts that read `--console-status-*` at runtime via `getComputedStyle` color
correctly in both themes with no per-theme code.

### Corner style (squarish, locked house style)

PF builds every radius from global primitives `--pf-t--global--border--radius--{100..500}`
plus semantic/role aliases. `tokens.css` overrides the primitives AND the aliases (small/
medium/large/tiny/pill + action/control roles) to **2px**, once, so the whole component set
(cards, buttons, inputs, tabs, chips) squares up together - no per-component CSS, and it
survives version bumps.

## Class-vs-ID convention

- **`pf-v6-*` classes are the ONLY borrowed class vocabulary.** Consume PF component/layout/
  utility classes as-is; do not re-skin, alias, or invent custom presentational classes that
  overlap them.
- **App hooks are IDs and `data-*`, never new classes** (`id="console-tabs"`, `data-tab-id`,
  `data-card`).
- **Accessibility is semantic elements + ARIA**, orthogonal to the classes: keep
  `<header>/<main>/<footer>` landmarks, real `<button>`, `role`/`aria-*`.
- **Escape hatch:** one small audited `overrides.css` for a genuinely PF-less bit. Prefer a
  `pf-v6-u-*` utility or an ID-scoped rule first.

## Naming methodology (STRICT - the formula for every class we author)

PatternFly owns the `pf-v6-*` vocabulary; we consume it as-is and invent NOTHING that overlaps
it. But some bits have no PF component (the status bar, the ANSI log body, the graph stage, the
gantt, the keybinding table, ...) and we must author classes for them. Every such class MUST
follow the formula below - as disciplined, prefixed, and greppable as PatternFly's own names -
so the custom surface stays tiny, self-documenting, collision-proof, and mechanically
maintainable. There are NO bare, ad-hoc, or unprefixed class names. This mirrors PF's
`pf-v6-c-<block>__<element>` + `pf-m-<modifier>` BEM structure.

### The formula

    console-<area>-<block>[__<element>][--<modifier>]

- **`console-`** - the app namespace (parallel to `pf-v6-`). EVERY custom class starts with it.
  A bare class like `.badge` or `.qchip` is forbidden; `grep -r "class=" | grep -v "pf-v6-\|console-"`
  must eventually return nothing but real HTML attributes.
- **`<area>`** - the region/surface that OWNS the class (parallel to PF's `c`/`l`/`u` slot).
  The allowed areas are a CLOSED set - pick exactly one:
  - `console-shell-*` the app frame: title bar, tab strip, left navigation rail, status bar,
    floating gear + settings popover, command palette, keybindings overlay, tiling.
  - `console-dashboard-*` the dashboard surface (hero, tiles, gantt, pool, stat strips, tables).
  - `console-log-*` the log viewer surface (filter chips, toolbar bits, zoom control).
  - `console-graph-*` the graph explorer surface (stage, sidebar, node cloud, legend, explain card).
  - `console-activity-*` the activity surface (only what is not already shared render).
  - `console-diff-*` the review surface (the virtualized hunk stream, its gutters and split
    columns, the file sidebar). Authored rather than PF because PF has no diff component, and
    because the row geometry is load-bearing: the stream is virtualized against a fixed row
    height, so these rules are part of the scroll math rather than decoration.
  - `console-plan-*` the delegation-plan surface (the unit-tree stage and its edges, the unit
    list that is the stage's accessible twin, the detail sheet). Authored for the same reason as
    the graph stage: PF has no component for a laid-out node/edge drawing, and the node geometry
    is shared with the layout that places it.
  - `console-render-*` the SHARED render model reused by log + activity (foldable sections,
    status badges, ANSI spans) - one home so both surfaces stay in lockstep.
- **`<block>`** - the component/thing, kebab-case, verbose and explicit. Prefer a full word to an
  abbreviation: `console-log-filter`, `console-shell-statusbar`, `console-dashboard-gantt`,
  `console-graph-nodelist`, `console-render-badge`, `console-render-ansi`.
- **`__<element>`** - a PART of the block (BEM double-underscore): `console-shell-statusbar__dot`,
  `console-log-filter__chip`, `console-dashboard-gantt__bar`, `console-graph-nodelist__pill`.
  Elements do NOT nest in the name (never `__row__cell`); flatten to `__cell` under the block.
- **`--<modifier>`** - a fixed structural/categorical VARIANT (BEM double-hyphen), used ONLY for a
  closed enumerated set: `console-render-ansi__fg--red`, `console-render-badge--pass`,
  `console-dashboard-gantt__bar--failed`. Do NOT use `--modifier` for transient STATE.

### State is `data-*`, not a class

This is what keeps the closed convention closed. Transient/boolean state (active, collapsed,
focused, capturing, hidden, selected, a live/health value) is a `data-*` attribute on the
element, styled as an attribute selector - NEVER a `--modifier` class. This matches the existing
app-hook convention (`data-state`, `data-health`, `data-collapsed`, `data-focus`). So:
`console-shell-statusbar__dot[data-state="connected"]`, `console-dashboard-tile[data-collapsed]`.
Reserve `--modifier` for the fixed vocabularies where an enumerated class reads better (the 6
ANSI colors, the badge kinds, the gantt bar kinds).

### IDs, data-* hooks, and PF classes are already fine - do not rename them

`#console-titlebar`, `#console-statusbar`, `#console-tabs`, `#console-outlet`, `data-tab-id`,
`data-pane-id`, `data-open`, `data-card`, every `pf-v6-*` - all stay. The formula governs only
the custom CSS CLASSES we author. A JS "hook" that carries no styling should be a `data-*`
attribute, not a class, wherever practical.

`data-surface` is SPOKEN FOR: it marks a mounted surface ROOT, and `console.css` styles several by
value (`[data-surface="home"]`, `[data-surface="actions"]`, ...). Chrome that lives inside
`#console-outlet` but is not a surface must pick its own hook - the navigation rail uses
`data-rail-surface` for exactly this reason, having first been written with `data-surface` and
silently inherited the Shortcuts surface's layout. Check a new hook against the existing selectors
before reusing a name that reads as generic.

### Examples (ad-hoc -> the convention)

    .a-fg-red        -> .console-render-ansi__fg--red
    .a-bold          -> .console-render-ansi--bold
    .badge-pass      -> .console-render-badge--pass
    .log-section     -> .console-render-section
    .status-item     -> .console-shell-statusbar__item
    .conn (dot)      -> .console-shell-statusbar__dot   (+ [data-state]/[data-health])
    .dash-hero       -> .console-dashboard-hero
    .gantt-bar       -> .console-dashboard-gantt__bar   (+ --running/--failed/... variants)
    .node-pill       -> .console-graph-nodelist__pill
    .k-<kind> dot    -> .console-graph-legend__swatch   (+ data-kind="<kind>")
    .sw-toast        -> .console-shell-toast
    .qchip           -> .console-log-filter__chip
