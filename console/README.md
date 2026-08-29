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

### Surface chrome (one bar, one head, one gutter)

Every surface opens with a strip, and each one used to size its own: measured across the nine
surfaces they came out 38, 40, 46, 52, 54, 57 and 68px tall, over five different inline gutters.
Three tokens decide it now, and a surface sheet reads them rather than picking a number.

| Token                       | What it sizes                                                     |
| --------------------------- | ----------------------------------------------------------------- |
| `--console-surface-bar-h`   | a BAR: the strip carrying a surface's controls (Runs filter, log viewer toolbar, Notes filter, graph stage header, plan toolbar). Derived from `--console-control-block-size`, so it is exactly a default control plus a symmetric spacer pair |
| `--console-surface-head-h`  | a HEAD: the strip carrying a label over a column (Activity's Events/Details, the diff's file index and its REVIEW head, the log viewer's Recent runs/Output) |
| `--console-pad`             | the inline gutter for every surface-level strip AND the content under it, so a header label starts on the same x as what it heads |

Use a bar's height as a FLOOR (`min-block-size`), never a fixed size: these rows wrap in a narrow
pane and have to be free to grow. Zero the strip's own `padding-block` when you do, or the two stack
and the row comes out taller than every other bar again.

`--log-pad` (logs) and `--notes-pad` (notes) still exist as local names, both defined as
`var(--console-pad)`. They are the re-scaling seam, not a second opinion: the dashboard's activity
tile and Big Picture narrow `--log-pad` for a preview that is not a whole page.

A strip's hairline must reach both edges of the region it heads. That means the CHILD spends the
gutter, not the container - a container's inline padding holds the child's `border-block-end` short
at each end, which is how the diff's head hairline came to stop 8px before the rail and 8px before
the sidebar/stream seam while its comment claimed a straight line across the surface.

Two families, and the difference is deliberate. A CHROME surface (runs, logs, graph, diff, notes,
activity, plan) paints a full-bleed bar and lets its content meet the pane edge. A DOCUMENT surface
(dashboard, settings, shortcuts) is a padded scrolling page whose content is inset. Both take their
inset from `--console-pad`, so content starts on the same x either way.

### Control size and type (`data-control-size`)

Two tiers, and which one a control takes is decided by WHERE it sits, not by the surface it belongs
to. A container declares its tier once with `data-control-size`; `tokens.css` then sizes every
`pf-v6-c-button`, toggle-group button, tabs link and form control inside it.

| Tier      | Height | Type | Where                                                                 |
| --------- | ------ | ---- | --------------------------------------------------------------------- |
| `default` | 37px   | 14px | a surface BAR: the Runs filter row, the log viewer toolbar, the graph stage header, the Notes filter, the diff's remark composer, the dashboard's own control row |
| `compact` | 29px   | 12px | an in-panel RAIL, HEAD or card: the graph sidebar, the log viewer's run index, the diff file index and the diff head's own actions, a dashboard card's own controls, the title bar's tray |

Neither number is picked - both restate PatternFly's own button formula (one line box plus its
vertical control spacer) at the default and compact steps.

The tier APPLIES the size rather than only publishing it. Publishing it was not enough: the token
reached five containers and every control inside still had to opt in by hand, so the log viewer's
filter row ran a 37px input beside 29px buttons - one row's controls disagreeing with each other.
Text inputs are in the set for that reason; a `textarea` is excluded (its height is its rows), and
so is `pf-m-inline`, which is a link inside a sentence rather than a control on a row.

Before this, four surfaces carried eight control heights (21, 22, 24, 25, 26, 28, 29, 37) and five
control font sizes (14, 12.48, 12, 11.52, 10.88px). `magus/control-size-token` pins both halves.

Its reach is PARTIAL and worth knowing: stylelint has no DOM, so the rule only fires on a rule whose
SELECTOR mentions `[data-control-size]`. A per-surface override written against a `console-*` class
still lands on a tiered control without being caught. The tiers are the fix; the rule is a backstop.

### The uppercase label (`--console-label-*` / `--console-chip-*`)

The console has exactly two uppercase voices, and `magus/label-token` (stylelint, tested in
`scripts/stylelint-label-token.test.mjs`) fails the build on a third. Written out by hand this ran
to 35 near-misses across seven sheets - six font sizes between 0.62 and 0.72rem, six tracking values
between 0.04 and 0.09em, three weights - with adjacent labels on one surface disagreeing.

- **`--console-label-size` / `-weight` / `-tracking`** - a section, column, facet, stat or panel
  label. Case and colour are all that separate it from the content around it.
- **`--console-chip-size` / `-weight` / `-tracking`** - the run inside a chip: a status badge on a
  log line, a scope pill, a run's verdict. Smaller and heavier, because the fill or outline it sits
  on already does the separating.

Write `text-transform: uppercase` yourself - it is the label's defining property, and the rule keys
on it to require the three tokens. Which recipe an element takes is the author's call; what is not
optional is taking one of them.

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
  - `console-plan-*` the lease-plan surface (the lease-tree stage and its edges, the lease
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
