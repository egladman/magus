---
title: magus-diagram
generated_from: internal/agent/skills/magus-diagram/SKILL.md
description: "Write, compose, check and view an architecture figure with flow, the Buzz diagram library: declared boxes and connectors, a claim on every connector (imports, calls or flow), a scope every package under it answers to, and a layout nobody places by hand."
tags: [agents, skills, magus-diagram]
skill_full_bytes: 15065
skill_short_bytes: 12058
---

# magus-diagram

Write, compose, check and view an architecture figure with flow, the Buzz diagram library: declared boxes and connectors, a claim on every connector (imports, calls or flow), a scope every package under it answers to, and a layout nobody places by hand. Use when a doc or review needs a picture of one subsystem, process or package scope, when composing a figure over the observed import set, when flow or the drift check refuses a figure and names the call to change, and when reading the console's Diagrams page. Do NOT use to draw the whole workspace, to place boxes by coordinate, or to produce Mermaid; for Buzz syntax itself use magus-buzz-write.

Install it, rather than copying from this page:

```sh
magus agent install .claude/skills   # writes both forms below
```

An installed copy carries a provenance stamp, so `magus doctor` can tell you when a magus upgrade has made it stale. Text copied from this page carries none.

## What an installed copy carries

`magus agent install` writes this frontmatter above the body. `magus doctor` reads it to report whether your installed skills are current.

| field | value |
| --- | --- |
| `license` | `GPL-3.0-or-later` |
| `compatibility` | `any-agent` |
| `source` | `magus` |
| `agent-skill-version` | `93` |
| `knowledge-schema-version` | `15` |
| `skill-content` | `430d77cbe4ce` |
| `skill-variant` | `full` |

The `skill-content` digest covers this skill alone, and both forms below report it: they go stale together, never one silently, and a change to another skill does not move it.

## The two forms

Both are hand-authored from one source body. The short form is the always-loaded primary - the enumeration dropped, the judgment kept, for the most capable readers rather than the least. The full form is its `<name>-full` twin, loaded by name when a reader wants the rationale. The bar above shows how much shorter the primary is; switch between them here to see exactly what it gave up. See [Skills](../../guides/integrations/agents/skills.md) for how to choose.

<article class="landing-tabs">
<header>
<input type="radio" name="magus-diagram-variant" id="magus-diagram-tab-short" checked>
<label for="magus-diagram-tab-short">Short form</label>
<input type="radio" name="magus-diagram-variant" id="magus-diagram-tab-full">
<label for="magus-diagram-tab-full">Full form</label>
</header>

<section class="landing-tabpanel">

```sh
magus agent install --tar | tar -xO -f - magus-diagram/SKILL.md
```

````markdown
# Drawing architecture figures with flow

flow is a Buzz library that lays out an architecture figure from what you declare:
boxes, connectors, what each connector claims about the code, and which part of the
tree the figure depicts. You never place anything. flow places it, refuses a
figure it cannot draw cleanly with a finding that names the call to change, and a
drift check fails the build when the code moves out from under the picture.

## When to reach for it

Draw ONE claim per figure: a subsystem, a process, or a package scope, stated in
the title, with the boxes and connectors as its evidence.

- Reach for it when prose keeps restating how parts connect, when a doc page
  describes a request path or a pipeline, or when a reviewer needs to see which
  packages a change crosses.
- Never draw everything. A figure of the whole workspace blows every budget and
  proves nothing.
- Before writing one, look for an existing figure of the same scope with
  `magus query diagrams`.

Budgets: at most 9 boxes, 12 connectors, 3 zones, 2 accented elements, and 14
characters per connector label.

## Where the library lives

flow is Buzz SOURCE, not a host module: `flow.buzz` and the renderer
`diagram.buzz` beside it, imported by their workspace path. `magus describe
modules` does not list them. In magus's own tree they are `libs/diagram/flow` and
`libs/diagram/diagram`.

A workspace that carries no copy has nothing to import
(`module not found`); the console still draws its graph from the server's
embedded copy. Run `magus describe file <path>/flow.buzz` before you
write a figure: it confirms the copy exists and names the targets that read it,
which are the workspace's figure targets.

## The first figure

A figure is one exported function returning a `Diagram`. The import lines
resolve against the workspace root, so run the file from there:

```buzz
import "libs/diagram/diagram" as _;
import "libs/diagram/flow" as _;

export fun cacheReadDiagram() > Diagram !> str {
    return flow("cache-read")
        .title("A hit skips the run")
        .node("key", label: "Cache key", role: Role.focal, anchor: "internal/cache")
        .node("store", label: "Local store", role: Role.store)
        .node("run", label: "Run target")
        .edge("key", dst: "store", label: "lookup")
        .edge("store", dst: "run", label: "miss")
        .legend(Role.store, label: "persistent")
        .scope(["internal/cache"])
        .diagram();
}
```

`.diagram()` lays it out and throws every finding at once; `.svg(lightPalette())`
does the same and renders it. Prove a figure draws before you register it: call
the function from a scratch `main()` inside `try`/`catch (e: str)` and run it
with `magus buzz <file>`. A clean figure returns; a refused one prints one
`flow "<id>": ...` line per finding.

## Roles are the only styling words

A box's `role:` and a connector's `role:` pick its treatment from the design
system. There is no color, font, or stroke argument, and there will not be one.

`Role` is default, focal, store, external, input, optional or decision;
`EdgeRole` is default, focal, external or optional. The enum declarations in
`flow.buzz` are the authority.

Spend the two accents on the one point the title makes. Give every role you use a `.legend(Role.x, label:)` line.
flow does not check the legend, so a missing one is silent.

A trust boundary is a group, not a box: `.boundary(label, members:)`. `.zone(label,
members:)` stacks its members into one band, bands in declaration order, and a
node sits in at most one zone or boundary. A zone groups and does nothing else.

## Every connector claims something

`claim:` says what the connector asserts about the code, and each claim has its
own evidence:

| `Claim` | use it for | checked against |
| --- | --- | --- |
| `Claim.imports` | the source package imports the destination | the import graph, under `--verify-imports`; both ends must be anchored |
| `Claim.calls` | a call across a process or network boundary: HTTP, RPC, a socket, a hook running a binary | declared and counted; no index can see it |
| `Claim.flow` | narrative, the default | never checked, counted as unverified |

Use the strongest claim that is true. A figure whose connectors are all
`Claim.flow` reads as unverified in the drift summary. Never claim `imports` for something you
believe rather than something the code does.

## Scope and omit

`anchor:` ties a box to a path in the workspace, a package or a file (a file
covers its package), and the drift check holds the box to it. Anything outside
the workspace takes `link:` instead, never both.

`.scope([...])` names the directories the figure depicts, one whole-segment glob
per entry (`internal/handler/*`, `internal/graph/**`). Every package under scope
must be anchored by a box or left out with `.omit(path, why:)`. A figure with no
code behind it, a decision process or a CI arrangement, says so with
`.unscoped(why:)`.

The reason is required, and it is for the next reader: it is the only record of
why the picture is missing a package the code has. Write it as a fact about the
code ("a read route, inside the read views box"), not as "not relevant".

## Compose over the observed set

When the scope is real code, do not hand-draw the imports. An observe target
reads the symbol index and writes the figure's OBSERVED half: every package under
the scope and the imports between them, stamped. In magus's own tree that is
`magus run diagrams-observe docs`, after `magus graph build`; it refuses
without an index rather than write an empty observation. Pass the result to
`flow` and declare only what a person knows:

```buzz
return flow("guard-path", observed: guardPathGen\guardPathObserved())
    .title("One command judges every call")
    .node("builtin", label: "Built-in rules", role: Role.focal, anchor: "internal/guard")
    .node("jobs", label: "Job store", role: Role.store, anchor: "internal/job")
    .node("hint", label: "next: breadcrumb", anchor: "internal/hint")
    .edge("builtin", dst: "jobs", label: "lease", claim: Claim.imports)
    .omitEdge("jobs", dst: "hint", why: "job results carry their own next steps")
    .legend(Role.store, label: "the leases")
    .scope(["internal/guard", "internal/job", "internal/hint"]);
```

The merge rule, which decides what you get:

- A declared box with an observed box's id or anchor MERGES with it, and your
  label, role and sub win. After the merge your id is the one every later call
  uses.
- Observed boxes and imports you do not mention are drawn as they are.
- `.omitEdge(src, dst:, why:)` hides one observed import, reason required. It
  survives every regeneration because it lives in your file, not the generated
  one.
- A declared connector over an observed import must claim `Claim.imports`.

Import the generated module ALIASED (`as guardPathGen`), never flat.

## Shape: rank, row and order

flow takes no coordinates and no weights. Three declarations relate boxes to each
other instead, and each is honored or refused, never silently ignored:

| call | does | refused when |
| --- | --- | --- |
| `.rank([...])` | puts the boxes in one column | a path already orders two of them |
| `.row([...])` | lines boxes up on one midline across columns, so connectors run straight | two share a column, or sit in different bands |
| `.order([...])` | sorts boxes within their shared column, first to last | they sit in different columns, fight the zone order, or form a cycle |

```buzz
flow("review")
    .title("Two checks, one verdict")
    .unscoped(why: "a process a reviewer follows")
    .node("diff", label: "Diff", role: Role.input)
    .node("lint", label: "Lint")
    .node("test", label: "Test")
    .node("verdict", label: "Verdict", role: Role.focal)
    .edge("diff", dst: "lint")
    .edge("diff", dst: "test")
    .edge("lint", dst: "verdict")
    .edge("test", dst: "verdict")
    .order(["test", "lint"])
    .row(["diff", "test", "verdict"]);
```

Reach for these only after the default layout reads badly.

## Findings name the call to change

Nothing is drawn until the figure is clean, and every finding says which call to
edit. Real ones, from flow:

```text
flow "guard-path": edge "builtin" -> "hint" replaces an observed import but claims flow; claim Claim.imports or omitEdge it
flow "guard-path": omitEdge "hint" -> "builtin" names no observed edge; drop the omitEdge
flow "review": rank(["diff", "verdict"]) contradicts the path "diff" -> "verdict"; drop one of them from rank([...])
```

Do what the finding says, at the call it names. A stale `omitEdge` means the code
changed under the figure: drop the override and look at what the code does now,
never re-point it at another edge to make the finding go away.

## The drift check

flow checks a figure's own consistency. The drift check holds it to the tree, and
it runs before a single SVG is written:

- every anchor still exists;
- every package under scope is anchored or omitted;
- every omit names a path that still exists;
- with `--verify-imports`, every `Claim.imports` connector against the import
  graph, and every observed stamp against a fresh observation.

In magus's own tree it is `magus run diagrams-generate docs`, and `magus run
diagrams-generate docs -- --verify-imports` after `magus graph build`. It prints a
summary line per figure:

```text
t: 3 nodes, 2 anchored; edges: 1 imports, 1 calls, 2 flow (unverified); imports and observed stamp unverified (run with --verify-imports)
```

Read that line, not only the exit code: a green run with every connector `flow
(unverified)` has checked the anchors and nothing else. `--verify-imports` refuses
when the import graph answers `indexed=false`, which means no symbol index was
ingested, not that there are no imports. Build the index and rerun; never drop
the flag to get past it.

A stale observed stamp means the code's imports moved since the observed half
was written: rerun the observe target and read what it changed before you touch
the declared file.

## Register and embed

A figure nobody registers is checked by nothing. Find the workspace's registry
with `magus query diagrams`; in magus's own tree it is the docs project's
figure list, where a figure needs its import plus an entry in the list, and a
composed figure also its aliased observed import and an observed-lookup entry.
A docs page then embeds it by id with an HTML comment, `<!--diagram:<id>-->`.

Source code can point back at a figure with a `magus:diagram <id>` comment beside
the code a box depicts; the drift check reports a marker whose figure is gone.

## The console

The console's Diagrams page draws the workspace itself, with no figure file: the
project graph, one project's targets, and the package import graph, each through
a declared lens of scope, focus and depth. It needs a running server
(`magus server start`), and the import view needs the symbol index. The server
renders with the same flow, so a lens over the budget is refused with flow's own
finding: narrow the lens rather than asking for a bigger picture. Anchored boxes
link to their source at the checkout's revision when the remote is on GitHub, and
carry no link otherwise.

## What this skill refuses

Each is a pitfall another diagram tool fell into, and none is a missing feature.

- **Coordinates, weights, pins.** A layout knob grows into a second language, and
  a picture that depends on hand placement goes stale the day the code moves.
  Shape a figure with rank, row and order, or split it.
- **Inferred or implied edges.** A connector nobody declared asserts something
  nobody checked. Every edge is declared or observed, and claims its kind; a
  relationship the index cannot see is `Claim.calls`, declared, never guessed.
- **Mermaid, out or in.** Emitting Mermaid would tie flow to the layout
  constraints it replaces, and accepting Mermaid syntax would inherit its bugs,
  such as a declared direction silently ignored. flow is its own format.

Writing the Buzz itself, the syntax and the strict-mode rules: magus-buzz-write.
````


</section>

<section class="landing-tabpanel">

```sh
magus agent install --tar | tar -xO -f - magus-diagram-full/SKILL.md
```

````markdown
# Drawing architecture figures with flow

flow is a Buzz library that lays out an architecture figure from what you declare:
boxes, connectors, what each connector claims about the code, and which part of the
tree the figure depicts. You never place anything. flow places it, refuses a
figure it cannot draw cleanly with a finding that names the call to change, and a
drift check fails the build when the code moves out from under the picture.

## When to reach for it

Draw ONE claim per figure: a subsystem, a process, or a package scope, stated in
the title, with the boxes and connectors as its evidence. A figure that
needs more than the budgets below is two figures, an overview and a detail, each
its own `flow()`; the budgets are the design system's, and past them nobody reads
the picture.

- Reach for it when prose keeps restating how parts connect, when a doc page
  describes a request path or a pipeline, or when a reviewer needs to see which
  packages a change crosses.
- Never draw everything. A figure of the whole workspace blows every budget and
  proves nothing; the console already draws the whole project graph
  through a lens, so a hand-written copy of it is a second, drifting answer.
- Before writing one, look for an existing figure of the same scope with
  `magus query diagrams`; two figures of one subsystem drift apart.

Budgets: at most 9 boxes, 12 connectors, 3 zones, 2 accented elements, and 14
characters per connector label.

## Where the library lives

flow is Buzz SOURCE, not a host module: `flow.buzz` and the renderer
`diagram.buzz` beside it, imported by their workspace path. `magus describe
modules` does not list them. In magus's own tree they are `libs/diagram/flow` and
`libs/diagram/diagram`.

A workspace that carries no copy has nothing to import: the import
fails at the first line with `module not found` rather than drawing anything. The
console's Diagrams page still works there, because the server evaluates its own
embedded copy; a running server answers that copy at `/api/v1/diagrams/source`. Run `magus describe file <path>/flow.buzz` before you
write a figure: it confirms the copy exists and names the targets that read it,
which are the workspace's figure targets.

## The first figure

A figure is one exported function returning a `Diagram`. The import lines
resolve against the workspace root, so run the file from there:

```buzz
import "libs/diagram/diagram" as _;
import "libs/diagram/flow" as _;

export fun cacheReadDiagram() > Diagram !> str {
    return flow("cache-read")
        .title("A hit skips the run")
        .node("key", label: "Cache key", role: Role.focal, anchor: "internal/cache")
        .node("store", label: "Local store", role: Role.store)
        .node("run", label: "Run target")
        .edge("key", dst: "store", label: "lookup")
        .edge("store", dst: "run", label: "miss")
        .legend(Role.store, label: "persistent")
        .scope(["internal/cache"])
        .diagram();
}
```

`.diagram()` lays it out and throws every finding at once; `.svg(lightPalette())`
does the same and renders it. Prove a figure draws before you register it: call
the function from a scratch `main()` inside `try`/`catch (e: str)` and run it
with `magus buzz <file>`. A clean figure returns; a refused one prints one
`flow "<id>": ...` line per finding.

The chain reads top to bottom as the figure's argument: `title` is the
claim, `desc` the sentence under it, `eyebrow` the small label above. `down()`
lays ranks top to bottom instead of left to right. Nothing else changes the
layout.

## Roles are the only styling words

A box's `role:` and a connector's `role:` pick its treatment from the design
system. There is no color, font, or stroke argument, and there will not be one.

| `Role` | meaning |
| --- | --- |
| `Role.default` | an ordinary part of what the figure depicts |
| `Role.focal` | the accent: the one point the figure makes |
| `Role.store` | something persistent |
| `Role.external` | outside what the figure depicts |
| `Role.input` | data arriving from outside |
| `Role.optional` | dashed: present only sometimes |
| `Role.decision` | a diamond with at most three exits and no tag or sub |

`EdgeRole` is `default`, `focal` (the accent), `external` (a call leaving the
system) or `optional` (dashed). A back edge draws dashed whatever its role,
because it runs against the flow.

Spend the two accents on the one point the title makes: a focal box
and a focal connector are both accents, and a third one means the figure is
making two points. Give every role you use a `.legend(Role.x, label:)` line.
flow does not check the legend, so a missing one is silent.

A trust boundary is a group, not a box: `.boundary(label, members:)`. `.zone(label,
members:)` stacks its members into one band, bands in declaration order, and a
node sits in at most one zone or boundary. A zone groups and does nothing else.

## Every connector claims something

`claim:` says what the connector asserts about the code, and each claim has its
own evidence. The point is that a reader can tell a checked connector
from a story:

| `Claim` | use it for | checked against |
| --- | --- | --- |
| `Claim.imports` | the source package imports the destination | the import graph, under `--verify-imports`; both ends must be anchored |
| `Claim.calls` | a call across a process or network boundary: HTTP, RPC, a socket, a hook running a binary | declared and counted; no index can see it |
| `Claim.flow` | narrative, the default | never checked, counted as unverified |

Use the strongest claim that is true. A figure whose connectors are all
`Claim.flow` reads as unverified in the drift summary, which is the
honest answer: nothing backs it. Never claim `imports` for something you
believe rather than something the code does; the check exists to catch
exactly that belief going stale.

## Scope and omit

`anchor:` ties a box to a path in the workspace, a package or a file (a file
covers its package), and the drift check holds the box to it. Anything outside
the workspace takes `link:` instead, never both.

`.scope([...])` names the directories the figure depicts, one whole-segment glob
per entry (`internal/handler/*`, `internal/graph/**`). Every package under scope
must be anchored by a box or left out with `.omit(path, why:)`. A figure with no
code behind it, a decision process or a CI arrangement, says so with
`.unscoped(why:)`.

The reason is required, and it is for the next reader: it is the only record of
why the picture is missing a package the code has. Write it as a fact about the
code ("a read route, inside the read views box"), not as "not relevant".

## Compose over the observed set

When the scope is real code, do not hand-draw the imports. An observe target
reads the symbol index and writes the figure's OBSERVED half: every package under
the scope and the imports between them, stamped. In magus's own tree that is
`magus run diagrams-observe docs`, after `magus graph build`; it refuses
without an index rather than write an empty observation. Pass the result to
`flow` and declare only what a person knows:

```buzz
return flow("guard-path", observed: guardPathGen\guardPathObserved())
    .title("One command judges every call")
    .node("builtin", label: "Built-in rules", role: Role.focal, anchor: "internal/guard")
    .node("jobs", label: "Job store", role: Role.store, anchor: "internal/job")
    .node("hint", label: "next: breadcrumb", anchor: "internal/hint")
    .edge("builtin", dst: "jobs", label: "lease", claim: Claim.imports)
    .omitEdge("jobs", dst: "hint", why: "job results carry their own next steps")
    .legend(Role.store, label: "the leases")
    .scope(["internal/guard", "internal/job", "internal/hint"]);
```

The merge rule, which decides what you get:

- A declared box with an observed box's id or anchor MERGES with it, and your
  label, role and sub win. After the merge your id is the one every later call
  uses: above, `omitEdge("jobs", ...)` names the declared id even though
  the observation called it `pkg-internal-job`.
- Observed boxes and imports you do not mention are drawn as they are,
  as `Claim.imports`; above, `builtin -> hint` is drawn without being declared.
- `.omitEdge(src, dst:, why:)` hides one observed import, reason required. It
  survives every regeneration because it lives in your file, not the generated
  one.
- A declared connector over an observed import must claim `Claim.imports`;
  claiming `flow` there would present what the code does as a story.

Import the generated module ALIASED (`as guardPathGen`), never flat: a
flat import is skipped when its basename is already bound, and it shares its
basename with the declared figure.

## Shape: rank, row and order

flow takes no coordinates and no weights. Three declarations relate boxes to each
other instead, and each is honored or refused, never silently ignored:

| call | does | refused when |
| --- | --- | --- |
| `.rank([...])` | puts the boxes in one column | a path already orders two of them |
| `.row([...])` | lines boxes up on one midline across columns, so connectors run straight | two share a column, or sit in different bands |
| `.order([...])` | sorts boxes within their shared column, first to last | they sit in different columns, fight the zone order, or form a cycle |

```buzz
flow("review")
    .title("Two checks, one verdict")
    .unscoped(why: "a process a reviewer follows")
    .node("diff", label: "Diff", role: Role.input)
    .node("lint", label: "Lint")
    .node("test", label: "Test")
    .node("verdict", label: "Verdict", role: Role.focal)
    .edge("diff", dst: "lint")
    .edge("diff", dst: "test")
    .edge("lint", dst: "verdict")
    .edge("test", dst: "verdict")
    .order(["test", "lint"])
    .row(["diff", "test", "verdict"]);
```

Reach for these only after the default layout reads badly. The default
already ranks by longest path, reduces crossings and routes orthogonally through
the gaps; a constraint is for the one thing it cannot know, such as which of two
parallel routes a reader should meet first.

## Findings name the call to change

Nothing is drawn until the figure is clean, and every finding says which call to
edit. Real ones, from flow:

```text
flow "guard-path": edge "builtin" -> "hint" replaces an observed import but claims flow; claim Claim.imports or omitEdge it
flow "guard-path": omitEdge "hint" -> "builtin" names no observed edge; drop the omitEdge
flow "review": rank(["diff", "verdict"]) contradicts the path "diff" -> "verdict"; drop one of them from rank([...])
```

Do what the finding says, at the call it names. A stale `omitEdge` means the code
changed under the figure: drop the override and look at what the code does now,
never re-point it at another edge to make the finding go away.

The rest, and what each asks for:

| finding | change |
| --- | --- |
| `10 nodes exceeds the budget of 9; split into overview plus detail` | cut or fold boxes, or make two figures |
| `label "..." runs past 14 characters` | shorten it, or move the words to `desc()` |
| `names no code; add scope([...]) or unscoped(why:)` | say what the figure depicts |
| `node "x" anchors "p", which observed node "y" already carries` | override it with `node("y")`, or omit the path |
| `node "x" has 4 exits; a Role.decision takes 3` | nest a second decision |
| `row([...]) names "a" and "b", which share a rank` | use `order([...])` within one rank |
| `..., not a string; name the case` | pass `Role.x`, `EdgeRole.x` or `Claim.x`, never a string |
| `... layout bug, report it` | not yours to fix; report it with the figure |

## The drift check

flow checks a figure's own consistency. The drift check holds it to the tree, and
it runs before a single SVG is written:

- every anchor still exists;
- every package under scope is anchored or omitted;
- every omit names a path that still exists;
- with `--verify-imports`, every `Claim.imports` connector against the import
  graph, and every observed stamp against a fresh observation.

In magus's own tree it is `magus run diagrams-generate docs`, and `magus run
diagrams-generate docs -- --verify-imports` after `magus graph build`. It prints a
summary line per figure:

```text
t: 3 nodes, 2 anchored; edges: 1 imports, 1 calls, 2 flow (unverified); imports and observed stamp unverified (run with --verify-imports)
```

Read that line, not only the exit code: a green run with every connector `flow
(unverified)` has checked the anchors and nothing else. `--verify-imports` refuses
when the import graph answers `indexed=false`, which means no symbol index was
ingested, not that there are no imports; an empty graph would otherwise
read as "no drift" and pass every claim. Build the index and rerun; never drop
the flag to get past it.

A stale observed stamp means the code's imports moved since the observed half
was written: rerun the observe target and read what it changed before you touch
the declared file.

## Register and embed

A figure nobody registers is checked by nothing. Find the workspace's registry
with `magus query diagrams`; in magus's own tree it is the docs project's
figure list, where a figure needs its import plus an entry in the list, and a
composed figure also its aliased observed import and an observed-lookup entry.
A docs page then embeds it by id with an HTML comment, `<!--diagram:<id>-->`.

Source code can point back at a figure with a `magus:diagram <id>` comment beside
the code a box depicts; the drift check reports a marker whose figure is gone.

## The console

The console's Diagrams page draws the workspace itself, with no figure file: the
project graph, one project's targets, and the package import graph, each through
a declared lens of scope, focus and depth. It needs a running server
(`magus server start`), and the import view needs the symbol index. The server
renders with the same flow, so a lens over the budget is refused with flow's own
finding: narrow the lens rather than asking for a bigger picture. Anchored boxes
link to their source at the checkout's revision when the remote is on GitHub, and
carry no link otherwise, because a guessed link is worse than none.

## What this skill refuses

Each is a pitfall another diagram tool fell into, and none is a missing feature.

- **Coordinates, weights, pins.** A layout knob grows into a second language, and
  a picture that depends on hand placement goes stale the day the code moves.
  Shape a figure with rank, row and order, or split it.
- **Inferred or implied edges.** A connector nobody declared asserts something
  nobody checked. Every edge is declared or observed, and claims its kind; a
  relationship the index cannot see is `Claim.calls`, declared, never guessed.
- **Mermaid, out or in.** Emitting Mermaid would tie flow to the layout
  constraints it replaces, and accepting Mermaid syntax would inherit its bugs,
  such as a declared direction silently ignored. flow is its own format.

Writing the Buzz itself, the syntax and the strict-mode rules: magus-buzz-write.
````


</section>

</article>
