# The console

The native console PWA: a standalone pnpm project, built and served independently of the
docs site. This file is the conventions the rest of the console's source cites by name:
the stylesheet stack, the token map, and the naming rules every authored class follows.

## Where code goes

- `src/apps/<id>/` is one launcher tile. `app.ts` exports its typed manifest (id, label, hint,
  glyph, motion, path, modes, and how the shell loads it); `main.ts` is its entry.
  `src/apps/index.ts` imports every manifest by hand.
- `src/desktop/` is the frame that belongs to no app: the launcher, tabs, tiling, the rail, the
  command bar, keybindings and the router.
- `src/render/`, `src/lib/`, `src/ui/` and `src/styles/` are shared by both.

Where a new piece goes:

- An **app** is a job you sit down to do with its own data. It gets a directory under `apps/`,
  so a tile, a rail row and an Open command.
- A **Dashboard tile or mode** is a status readout: live state at a glance, linking to the app
  that owns the data.
- A **view** is a lens inside the app that owns the data. It lives in that app's directory and is
  one of its modes, never a tile: the Dashboard's Jobs view (`apps/dashboard/plan/`), the Graph's
  Figures (`apps/graph/diagrams/`). A manifest's `modes` map a served segment to one, so
  `/console/plan/` and `/console/diagrams/` still open them.

The directory is the list. The build bundles every `apps/*/main.ts` except a shell-loaded app's
into `gen/<id>/<id>.js`, `scripts/app-stubs.mjs` writes a stub for each manifest's segments,
the server serves exactly those stubs, and the router reads the manifests. The one list outside
the tree is the server's link vocabulary, `KnownApps` in `internal/service/console/url.go`,
because the CLI mints links with no console built. `src/apps/apps.test.ts` fails when any of them
disagrees with the directories, in either direction.

## Styling standard

Normative. A new sheet, view or component follows it; a reviewer rejects a change that does not. The
rest of this file says how each rule is built.

- **Type** is the PF tokens and nothing else, and `magus/type-token` allows exactly these. Never a
  numbered step (`font--size--100`, `font--weight--300`, `line-height--200`, `family--300`).
  - Size: `--pf-t--global--font--size--body--sm` (12px), `--body--default` (14px), `--body--lg` (16px);
    `--heading--h1` to `--heading--h6` and `--heading--xs` to `--heading--2xl`; the plain steps `--xs`
    (12px) to `--4xl`; `inherit`; an `em` of 1 or more or a `%` of 100 or more. Nothing is under
    12px, and no `rem` or `px` size is written by hand. The uppercase label and chip are
    `--console-label-size` and `--console-chip-size`, both mapped to `--body--sm`.
  - Weight: `--font--weight--body--default`, `--body--bold`, `--heading--default`, `--heading--bold`,
    and the `--legacy` variants PatternFly ships (`body--legacy`, `heading--legacy`,
    `body--bold--legacy`, `heading--bold--legacy`).
  - Leading: `--font--line-height--body` (1.5) or `--heading` (1.3). `0` and `1` are allowed for a
    glyph or icon box, as in PatternFly's own sheets; they are not leading for text. A row whose
    height is fixed by script takes a named `--console-*line-height` token.
  - Family: `--font--family--body`, `--heading` or `--mono`. The `font` shorthand passes only when
    every piece is one of these tokens, and `font: inherit` passes.
- **Spacing** names the specific semantic token first and falls back to the global steps only for
  text rhythm. PatternFly's rule: use the semantic token when one fits.
  - Padding inside a control (input, toggle, menu toggle): `--pf-t--global--spacer--control--horizontal--*`
    and `--control--vertical--*` (`default`, `compact`, `plain`, `spacious`).
  - Padding inside an action (button): `--spacer--action--horizontal--*` (PatternFly defines no
    vertical action spacer; a button's block padding is the control one).
  - Space between elements or groups: `--spacer--gap--*`. A gutter in a layout: `--spacer--gutter--default`.
    Inner padding of structural chrome such as the masthead or the page: `--spacer--inset--page-chrome`.
  - Text rhythm, such as a heading above body copy or the items of a list, and anything the roles above
    do not name: the global steps `--spacer--xs` (4px), `--sm` (8px), `--md` (16px), `--lg` (24px),
    `--xl` (32px), `--2xl` (48px), `--3xl`, `--4xl`. A value between two steps rounds to one of
    them; it does not get a new step.
  - `padding`, `margin`, `gap`, `inset`, `top`, `right`, `bottom` and `left` take `0`, `auto`, a
    percentage, one of these tokens or a `calc()` of them. Placement alone (`top`, `inset`, ...) may
    also name the size of a fixed shell bar or column (`--console-topchrome-h`, `--console-titlebar-h`,
    `--console-statusbar-h`, `--console-launcher-w`).
- **Take the semantic token, never a base or palette one.** `magus/token-tier` rejects any
  `--pf-t--...` token that ends in a number (`z-index--300`, `border--width--200`, `spacer--200`,
  `color--status--danger--100`) and any palette token (`--pf-t--color--*`) outside `tokens.css`, the one
  file that adapts them. Use `z-index--xs` to `--2xl`, `border--width--regular`, `--strong`,
  `--extra-strong`, `border--radius--tiny` to `--large`, and the status `--default` and `--hover`
  colours. A token the console needs and PatternFly has no semantic name for is defined once in
  `tokens.css` as a `--console-*` slot (`--console-syntax-*`, `--console-series-*`, `--console-radius-micro`,
  `--console-motion-*`).
- **Every token must exist.** `magus/token-exists` reads the installed `@patternfly/patternfly`
  (`patternfly-base.css` for the `--pf-t--` tokens, the component and layout sheets for
  `--pf-v6-c-*` and `--pf-v6-l-*`) and fails on a name that is declared or read but not defined there,
  naming the nearest real names. A misspelt token resolves to nothing and the declaration silently
  falls back, which is how `--pf-t--global--font--body--sm` and `--pf-v6-c-button--PaddingBlock` went
  unnoticed.
- **Shape and motion are tokens.** `magus/shape-token` takes `border-radius` from the
  `--pf-t--global--border--radius--*` aliases (or `0`, a percentage, or `--console-radius-*`), a border
  or outline width from `--pf-t--global--border--width--regular`, `--strong`, `--extra-strong` (or `0`
  and `none`), and a `transition` or `animation` duration from `--pf-t--global--motion--duration--*` or a
  `--console-motion*` token. Anything at or under 1ms is "motion off", which is PatternFly's own
  reduced-motion value, and is allowed.
- **Colour has no literals.** `magus/color-token` rejects hex, `rgb()`, `hsl()` and named colours
  (`red`, `white`), a `text-shadow` other than `none`, and a `filter: drop-shadow()`; a shadow is a
  `--pf-t--global--box-shadow--*` token or `none`. `transparent` and `currentcolor` are fine.
- **Status is never colour alone.** A state that has a colour also has a shape and a word: put
  `statusIcon` and `statusText` (or `statusMark`, both in `ui/status.ts`) beside the dot, or use an
  Alert, which carries an icon and a screen-reader severity prefix. A dot that only changes hue is a bug.
- **Every failure is a toast and, where the reader is looking, text.** Raise the toast with
  `reportFailure(source, message, key)` (`lib/notifications.ts`) or `showToast(..., "error")`, and put
  an inline Alert (`inlineAlert` in `ui/alert.ts`) in the pane that failed to render. A failure only a
  console log or a blank pane knows about is a bug. The toast is a PF Alert in one toast group; a link or
  button in it is not announced as interactive, so write the message to say where to go and what to do.
- **Pane-relative layout uses container queries** (a house rule, below). The console tiles panes, so a
  pane is narrower than the viewport and a viewport `@media (max-width)` answers the wrong question.
  Declare `container-type: inline-size` on the pane or app root and use `@container`. A viewport media
  query is right only for the shell itself (title bar, rail) and for `prefers-*` and `hover`/`pointer`
  features. `magus/container-query` enforces it in `src/apps/`.
- **Use the PF component when one exists**, and do not re-skin it. Imported now (`patternfly.css`), and
  every one of them is mounted by the console: Alert and Alert group, Backdrop, Badge, Bullseye, Button,
  Card, Check, ClipboardCopy, DataList, DescriptionList, Divider, Drawer, EmptyState, ExpandableSection,
  Form, FormControl, Gallery, HelperText, Icon, InputGroup, Label and Label group, Menu, MenuToggle,
  ModalBox, Nav, NotificationDrawer, Popover, Progress, Radio, Skeleton, Spinner, Switch, Table (with
  its grid variant), Tabs, TextInputGroup, ToggleGroup, Toolbar, TreeView. Add the sheet when an app
  starts using one more (see below), and drop it when the last use goes. Hand-rolled toasts, alerts,
  menus, tables and help popovers are what `ui/` and `lib/toast.ts` replace:

  | Need                | Use                                                            |
  | ------------------- | -------------------------------------------------------------- |
  | A transient message | `showToast`, `reportFailure`                                   |
  | An in-place message | `inlineAlert` (`ui/alert.ts`)                                  |
  | A state mark        | `statusIcon`, `statusText`, `statusMark` (`ui/status.ts`)      |
  | A menu              | PF Menu markup and `wireMenu` (`ui/menu.ts`)                   |
  | A menu's trigger    | `menuToggle` (`ui/menu-toggle.ts`), a PF MenuToggle            |
  | A "?" explanation   | `createHelpButton`, `attachHelpPopover` (`ui/help-popover.ts`) |
  | A sortable table    | `SortableTable` (`ui/table.ts`)                                |
  | An empty state      | `emptyStateShell` (`ui/empty-state.ts`)                        |
  | A show/hide section | `expandableSection` (`ui/expandable.ts`)                       |

- **One focus ring** (a house rule, below). The global
  `:where(button, a, [role="button"], [tabindex]):focus-visible` rule in `console.css` draws PF's
  focus-ring token at PF's offset. A sheet does not restate the colour or the width. Where an ancestor's
  `overflow` would clip an outside ring, set only `outline-offset: var(--console-focus-inset)`.
- **Hit areas** are at least 24px square for anything pressed (a house rule, below), and a `title=` is
  never the only place an explanation lives (touch has no hover).

### House rules beyond PatternFly

Everything above applies PatternFly's own guidance. These do not come from it. Each is the console's
decision, with the reason, so a reviewer can tell a rule to keep from a PatternFly default to follow.

- **Container queries, not viewport media queries.** PatternFly's own responsive components (Toolbar,
  Data list, Description list, Form, Drawer) respond to the viewport. Only Table has a container hook
  (`pf-v6-contain-table`). The console tiles panes, so a pane can be narrow in a wide window, and the
  viewport answers the wrong question. Where a PF component has no container hook, size its
  container instead of overriding its breakpoints.
- **The focus ring is the console's.** PF Core 6.5.2 draws no focus ring on a button, link or
  `tabindex` element; it only ships the focus-ring tokens. The global rule in `console.css` is
  the console's, and `tokens.css` points PF's focus-ring colour primitives at the brand ramp so the ring
  matches the buttons beside it.
- **24px targets.** WCAG 2.2 AA 2.5.8 asks for 24 by 24 CSS pixels. PatternFly's compact tier (29px
  tall) clears it, but a hand-built control did not, so the floor is stated for everything pressed.
  A coarse pointer raises the control tiers to 44px (`tokens.css`), which is Apple's guideline and
  stricter than WCAG.
- **Two uppercase voices.** PatternFly sets no uppercase label. The console has exactly the label
  (`--console-label-*`) and the chip (`--console-chip-*`), and `magus/label-token` fails a third, so
  adjacent labels cannot drift between sizes and tracking.
- **The compact tier is 12px type.** PatternFly's `pf-m-small` changes padding only and leaves the font
  at 14px. The `compact` control tier pairs the 29px height with `--font--size--body--sm`, so a dense
  rail does not mix 12px text with 14px controls.
- **Danger toasts are assertive.** A PF Alert in the toast group inherits the group's polite live
  region. A danger toast adds `role="alert"` (`lib/toast.ts`), so a failure interrupts a screen reader and
  a success or warning does not. The group is PF's own list: `ul.pf-v6-c-alert-group.pf-m-toast`
  (`role="list"`, `aria-live="polite"`, `aria-atomic="false"`) of `li.pf-v6-c-alert-group__item`.
- **At most five toasts.** A burst past `MAX_TOASTS` (5) dismisses the oldest rather than walling the
  page. Nothing is lost: the notification history keeps every entry.
- **Corner radii are 2-4px on containers, and the pill is 4px.** PatternFly's defaults are 6px, 16px,
  24px and a 999px pill. The console squares containers and chrome to 2-4px, keeps 6px for controls, and
  sets `--border--radius--pill` to 4px so a chip is a crisp rounded rectangle, not a cylinder (see
  Corner style).

## PatternFly

`@patternfly/patternfly@6.5.2` (devDependency, exact pin). PatternFly Core (CSS only, no
JS runtime), which is the documented path for non-React consumers. Prefix `pf-v6`; expect a
`pf-v6 -> pf-v7` churn at the next major, contained to the class strings and `tokens.css`.

PatternFly is the console's ONLY design system. The stylesheet stack, in load order:

1. `patternfly.css`: PF Core base + the per-component sheets we render.
2. `tokens.css`: the console's PF-native token layer (corner radii, the brand ramp and focus ring, system fonts, `--console-*` slots, the syntax palette).
3. `console.css`: the shell rules (title bar, navigation rail, status-bar frame vars, tiling,
   launcher, layout).
4. `overrides.css`: the small ID/class-scoped escape hatch for PF-less shell chrome.
5. Per app, lazily: every `apps/<id>/<id>.css` (`activity`, `dashboard`, `diff`, `graph`, `logs`,
   `notes`, `runs`, `tools`) bundles into `gen/<id>/<id>.css`. A view's sheet
   (`dashboard/plan/plan.css`, `graph/diagrams/diagrams.css`) rides its app's sheet by `@import`.
   Two sheets are shared by more than one app: `render/frame.css` is the log and activity frame
   (foldable sections, empty state, event index), imported by both `logs/logs.css` and
   `activity/activity.css`, and it imports `render/render.css`, which `dashboard/dashboard.css`
   imports too. `apps/activity/activity.css` is the Activity app's own sheet on top of that frame (the
   event head, the refresh notice, the busy state), so a change to the frame lands in both apps.

### How it is bundled

- `src/styles/patternfly.css` @imports the PF **base** plus only the **per-component** sheets
  we render (the list is under Styling standard). esbuild `--bundle --minify` inlines them into
  `gen/patternfly.css`. Add a component's sheet here when an app starts using it; that is
  the whole opt-in. Do NOT import the 1.8MB monolith `patternfly.min.css`.
- Font/image `url()` assets are marked `--external` in the build script so esbuild leaves the
  urls instead of inlining PF's ~10MB `assets/`. All such urls live in `patternfly-base.css`
  and are **token default values**, not referenced by the markup we emit; `tokens.css`
  overrides the RedHat body/heading/mono font tokens to a system stack, so those `@font-face`
  rules are never referenced and never fetched. If a later app renders pficon glyphs or a
  masthead background, it must ship a trimmed `assets/` subset or override those tokens too.
- `gen/patternfly.css` (~683KB minified) is the dominant CSS cost: the full `--pf-t-*` palette
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

These PF status tokens are **theme-aware**: they resolve to the right value in light and
dark, so charts that read `--console-status-*` at runtime via `getComputedStyle` color
correctly in both themes with no per-theme code.

### App chrome (one bar, one head, one gutter)

Every app opens with a strip, and each one used to size its own: measured across the nine
apps they came out 38, 40, 46, 52, 54, 57 and 68px tall, over five different inline gutters.
Three tokens decide it now, and an app sheet reads them rather than picking a number.

| Token                       | What it sizes                                                     |
| --------------------------- | ----------------------------------------------------------------- |
| `--console-app-bar-h`       | a BAR: the strip carrying an app's controls (Runs filter, log viewer toolbar, Notes filter, graph stage header, plan toolbar). Derived from `--console-control-block-size`, so it is exactly a default control plus a symmetric spacer pair |
| `--console-app-head-h`      | a HEAD: the strip carrying a label over a column (Activity's Events/Details, the diff's file index and its REVIEW head, the log viewer's Recent runs/Output) |
| `--console-pad`             | the inline gutter for every app-level strip AND the content under it, so a header label starts on the same x as what it heads |

Use a bar's height as a FLOOR (`min-block-size`), never a fixed size: these rows wrap in a narrow
pane and have to be free to grow. Zero the strip's own `padding-block` when you do, or the two stack
and the row comes out taller than every other bar again.

`--log-pad` (logs) and `--notes-pad` (notes) still exist as local names, both defined as
`var(--console-pad)`. They are the re-scaling seam, not a second opinion: the dashboard's activity
tile and Big Picture narrow `--log-pad` for a preview that is not a whole page.

A strip's hairline must reach both edges of the region it heads. That means the CHILD spends the
gutter, not the container; a container's inline padding holds the child's `border-block-end` short
at each end, which is how the diff's head hairline came to stop 8px before the rail and 8px before
the sidebar/stream seam while its comment claimed a straight line across the app.

Two families, and the difference is deliberate. A CHROME app (runs, logs, graph, diff, notes,
activity, plan) paints a full-bleed bar and lets its content meet the pane edge. A DOCUMENT app
(dashboard, settings, shortcuts) is a padded scrolling page whose content is inset. Both take their
inset from `--console-pad`, so content starts on the same x either way.

### Control size and type (`data-control-size`)

Two tiers, and which one a control takes is decided by WHERE it sits, not by the app it belongs
to. A container declares its tier once with `data-control-size`; `tokens.css` then sizes every
`pf-v6-c-button`, toggle-group button, tabs link and form control inside it.

| Tier      | Height | Type | Where                                                                 |
| --------- | ------ | ---- | --------------------------------------------------------------------- |
| `default` | 37px   | 14px | an app BAR: the Runs filter row, the log viewer toolbar, the graph stage header, the Notes filter, the diff's remark composer, the dashboard's own control row |
| `compact` | 29px   | 12px | an in-panel RAIL, HEAD or card: the graph sidebar, the log viewer's run index, the diff file index and the diff head's own actions, a dashboard card's own controls, the title bar's tray |

Neither number is picked; both restate PatternFly's own button formula (one line box plus its
vertical control spacer) at the default and compact steps.

The tier APPLIES the size rather than only publishing it. Publishing it was not enough: the token
reached five containers and every control inside still had to opt in by hand, so the log viewer's
filter row ran a 37px input beside 29px buttons, one row's controls disagreeing with each other.
Text inputs are in the set for that reason; a `textarea` is excluded (its height is its rows), and
so is `pf-m-inline`, which is a link inside a sentence rather than a control on a row.

Before this, four apps carried eight control heights (21, 22, 24, 25, 26, 28, 29, 37) and five
control font sizes (14, 12.48, 12, 11.52, 10.88px). `magus/control-size-token` pins both halves.

Its reach is PARTIAL and worth knowing: stylelint has no DOM, so the rule only fires on a rule whose
SELECTOR mentions `[data-control-size]`. A per-app override written against a `console-*` class
still lands on a tiered control without being caught. The tiers are the fix; the rule is a backstop.

### The uppercase label (`--console-label-*` / `--console-chip-*`)

The console has exactly two uppercase voices, and `magus/label-token` (stylelint, tested in
`scripts/stylelint-label-token.test.mjs`) fails the build on a third. Written out by hand this ran
to 35 near-misses across seven sheets: six font sizes between 0.62 and 0.72rem, six tracking values
between 0.04 and 0.09em, three weights, with adjacent labels on one app disagreeing.

- `--console-label-size`, `-weight` and `-tracking`: a section, column, facet, stat or panel
  label. Case and colour are all that separate it from the content around it.
- `--console-chip-size`, `-weight` and `-tracking`: the run inside a chip: a status badge on a
  log line, a scope pill, a run's verdict. Heavier, because the fill or outline it sits on already
  does the separating. Both sizes are the 12px body token, the floor for any text.

Write `text-transform: uppercase` yourself; it is the label's defining property, and the rule keys
on it to require the three tokens. Which recipe an element takes is the author's call; what is not
optional is taking one of them.

### Corner style (two tiers, locked house style)

PF builds every radius from numbered primitives (`--pf-t--global--border--radius--100` to `--500`)
behind semantic/role aliases. `tokens.css` overrides the aliases once and leaves the primitives alone,
so the whole component set follows with no per-component CSS, and it survives version bumps. A sheet
never names a primitive: `magus/token-tier` rejects one, and `magus/shape-token` takes only the aliases.
There are two tiers, not one radius:

- **Containers and passive chrome** (cards, menus, popovers, tabs, chips, labels): 2-4px
  (the tiny/small/medium/large aliases, with the pill alias also 4px), so panels stay crisp.
  `--console-radius-micro` (a fixed 2px that does not scale on Apple) is for marks under about 20px
  tall, such as a count badge or a divider grip.
- **Interactive controls** (buttons, inputs, selects; the `action`, `action--plain` and
  `control` role aliases): 6px, so they read as pressable against the panel they sit on.

On Apple platforms (`data-platform="apple"` on the root, set by `theme.ts`) the whole scale
is a step rounder, same tiers.

### Diagram figures (`--magus-diagram-*`)

magus/figure paints an inlined figure with `var(--magus-diagram-<role>, <light hex>)`. The block at
the end of `tokens.css` maps all nine roles (paper, fill, ink, muted, soft, rule, accent,
accent-tint, link) onto theme-aware slots, so a figure follows the console's light and dark themes
with no per-theme copy. `apps/graph/diagrams/view-dom.test.ts` fails if a role goes missing.

## Figures mode in the graph app (`src/apps/graph/diagrams/`)

A view inside the Graph app, bundled into `gen/graph/graph.js` and `graph.css`, over
`GET /api/v1/diagrams`. The Graph's Figures button, the `graph.figures.toggle` command and
`/console/diagrams/` open it in the explorer's place. The server's SVG is the page: it is inlined the moment it arrives, every
anchored node is a real `<a href>` to its source, and the node list beside it says the same thing
in words. `interact.ts` then only adds listeners:

- `f` fits, `+` `-` zoom, `0` is the figure's own size, Esc clears focus. Keys are read on the
  figure's frame, never on document, and a modified key is always the browser's.
- ctrl/cmd+wheel and pinch zoom about the pointer; a plain wheel scrolls the page; drag pans.
- Double-click (or Focus) keeps a node, its declared edges and one-hop neighbours, and dims the
  rest. Hover lights the declared edges. Nothing shows a relation the figure does not draw.
- Tab walks the nodes in reading order, one tabbable at a time; Enter follows the link.
- Reduced motion (the OS setting or the console's own) snaps instead of gliding.

The lens form (Figure, Scope, Center on, Depth) re-requests the figure and rides in the `#fragment`
(`diagram=`, `scope=`, `focus=`, `depth=`). A 422 (over budget), 409 (no symbol index) or 400
(bad lens) is the server's own sentence in an inline notice, and a toast, from `api.ts`.

**Load interactive runtime** is the only thing that loads the playground's Buzz wasm (4.2MB).
The build copies `../docs/gen/playground/{buzz.wasm,wasm_exec.js}` into `gen/wasm/` when docs'
`build_playground` has run, and says so when it has not. Once loaded, a lens change cuts the
declaration the server served (its nodes plus the SVG's `data-edge` set), builds the same
`figure\Figure` record `internal/handler/diagram` builds (Dir boxes for the import figure, actors
for the others), and hands it to `buzz.drawFigure`, which calls `figure\draw` in the wasm's own
magus/figure. The page writes no Buzz. It swaps the SVG in place with the zoom kept. The server's
CSP allows it with `'wasm-unsafe-eval'`. `wasm-real.test.ts` drives that path on the built wasm and
skips until `magus run build_playground docs` has run.

## Class-vs-ID convention

- **`pf-v6-*` classes are the ONLY borrowed class vocabulary.** Consume PF component/layout/
  utility classes as-is; do not re-skin, alias, or invent custom presentational classes that
  overlap them.
- **App hooks are IDs and `data-*`, never new classes** (`id="console-tabs"`, `data-tab-id`,
  `data-card`).
- **Accessibility is semantic elements + ARIA**, orthogonal to the classes: keep
  `<header>/<main>/<footer>` landmarks, real `<button>`, `role`/`aria-*`.
- **One small audited `overrides.css` is the escape hatch** for a genuinely PF-less bit. Prefer a
  `pf-v6-u-*` utility or an ID-scoped rule first.

## Naming methodology (strict: the formula for every class we author)

PatternFly owns the `pf-v6-*` vocabulary; we consume it as-is and invent NOTHING that overlaps
it. But some bits have no PF component (the status bar, the ANSI log body, the graph stage, the
gantt, the keybinding table, ...) and we must author classes for them. Every such class MUST
follow the formula below, as disciplined, prefixed, and greppable as PatternFly's own names,
so the custom set stays tiny, self-documenting, collision-proof, and mechanically
maintainable. There are NO bare, ad-hoc, or unprefixed class names. This mirrors PF's
`pf-v6-c-<block>__<element>` + `pf-m-<modifier>` BEM structure.

### The formula

```text
console-<area>-<block>[__<element>][--<modifier>]
```

- `console-`: the app namespace (parallel to `pf-v6-`). EVERY custom class starts with it.
  A bare class like `.badge` or `.qchip` is forbidden; `grep -r "class=" | grep -v "pf-v6-\|console-"`
  must eventually return nothing but real HTML attributes.
- `<area>`: the region/app that OWNS the class (parallel to PF's `c`/`l`/`u` slot).
  The allowed areas are a CLOSED set: pick exactly one:
  - `console-shell-*` the app frame: title bar, tab strip, left navigation rail, status bar,
    floating gear + settings popover, command palette, keybindings overlay, tiling.
  - `console-dashboard-*` the dashboard app (hero, tiles, gantt, pool, stat strips, tables).
  - `console-log-*` the log viewer app (filter chips, toolbar bits, zoom control).
  - `console-graph-*` the graph explorer app (stage, sidebar, node cloud, legend, explain card).
  - `console-activity-*` the activity app (only what is not already shared render).
  - `console-diff-*` the review app (the virtualized hunk stream, its gutters and split
    columns, the file sidebar). Authored rather than PF because PF has no diff component, and
    because the row geometry is load-bearing: the stream is virtualized against a fixed row
    height, so these rules are part of the scroll math rather than decoration.
  - `console-plan-*` the lease-plan app (the lease-tree stage and its edges, the lease
    list that is the stage's accessible twin, the detail sheet). Authored for the same reason as
    the graph stage: PF has no component for a laid-out node/edge drawing, and the node geometry
    is shared with the layout that places it.
  - `console-render-*` the SHARED render model reused by log + activity (foldable sections,
    status badges, ANSI spans); one home so both apps stay in lockstep.
- `<block>`: the component/thing, kebab-case, verbose and explicit. Prefer a full word to an
  abbreviation: `console-log-filter`, `console-shell-statusbar`, `console-dashboard-gantt`,
  `console-graph-nodelist`, `console-render-badge`, `console-render-ansi`.
- `__<element>`: a PART of the block (BEM double-underscore): `console-shell-statusbar__dot`,
  `console-log-filter__chip`, `console-dashboard-gantt__bar`, `console-graph-nodelist__pill`.
  Elements do NOT nest in the name (never `__row__cell`); flatten to `__cell` under the block.
- `--<modifier>`: a fixed structural/categorical VARIANT (BEM double-hyphen), used ONLY for a
  closed enumerated set: `console-render-ansi__fg--red`, `console-render-badge--pass`,
  `console-dashboard-gantt__bar--failed`. Do NOT use `--modifier` for transient STATE.

### State is `data-*`, not a class

This is what keeps the closed convention closed. Transient/boolean state (active, collapsed,
focused, capturing, hidden, selected, a live/health value) is a `data-*` attribute on the
element, styled as an attribute selector, NEVER a `--modifier` class. This matches the existing
app-hook convention (`data-state`, `data-health`, `data-collapsed`, `data-focus`). So:
`console-shell-statusbar__dot[data-state="connected"]`, `console-dashboard-tile[data-collapsed]`.
Reserve `--modifier` for the fixed vocabularies where an enumerated class reads better (the 6
ANSI colors, the badge kinds, the gantt bar kinds).

### IDs, data-* hooks, and PF classes are already fine; do not rename them

`#console-titlebar`, `#console-statusbar`, `#console-tabs`, `#console-outlet`, `data-tab-id`,
`data-pane-id`, `data-open`, `data-card`, every `pf-v6-*`: all stay. The formula governs only
the custom CSS CLASSES we author. A JS "hook" that carries no styling should be a `data-*`
attribute, not a class, wherever practical.

`data-app` is SPOKEN FOR: it marks a mounted app ROOT, and `console.css` styles several by
value (`[data-app="home"]`, `[data-app="shortcuts"]`, ...). Chrome that lives inside
`#console-outlet` but is not an app must pick its own hook: the navigation rail uses
`data-rail-app` for exactly this reason, having first been written with `data-app` and
silently inherited the Shortcuts app's layout. Check a new hook against the existing selectors
before reusing a name that reads as generic.

### Examples (ad-hoc -> the convention)

```text
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
.qchip           -> .console-log-filter__chip
```
