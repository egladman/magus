---
title: magus-diagram
generated_from: internal/agent/skills/magus-diagram/SKILL.md
description: "Write, check and view an architecture figure with magus/figure, the embedded Buzz module."
tags: [agents, skills, magus-diagram]
skill_full_bytes: 8186
skill_short_bytes: 5782
---

# magus-diagram

Write, check and view an architecture figure with magus/figure, the embedded Buzz module. Boxes come from the knowledge graph's own Dir records, groups from a declared layer or a dirs set, edges from imports and declared calls, and nobody places boxes by hand. Use when a doc or review needs a picture of one subsystem, process or package scope, when a figure refuses to draw and names the call to change, and when reading the console's Diagrams page. Do NOT use to draw the whole workspace or to place boxes by coordinate; for Buzz syntax itself use magus-buzz-lang.

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
| `agent-skill-version` | `140` |
| `knowledge-schema-version` | `22` |
| `skill-content` | `a193a1c0ab6a` |
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
# magus/figure

`magus/figure`: Buzz module in binary; lays out architecture figure from knowledge graph records.
Box: directory graph holds. Group: set of them.
Edges between code boxes: imports and declared calls those records carry.
Never place anything or type edge between two packages.
Module places figure, or refuses with finding naming call to change.

## When

One claim per figure: subsystem, process, or package scope, stated in title; boxes and connectors are evidence.
- Use when prose keeps restating how parts connect, or doc page describes request path or pipeline.
- Use when reviewer needs packages change crosses.
- Never draw everything: whole-workspace figure blows every budget, proves nothing.
- Before writing: find same-scope figure via `magus query diagrams`.

Max: 9 boxes, 12 connectors, 3 zones, 2 accented elements, 14 characters per connector label.

## First figure

Figure: one exported function returning `figure\Figure`.
Both imports resolve in any workspace; nothing needs copy on disk:

```buzz
import "magus";
import "magus/figure";

export fun serverHttpFigure() > figure\Figure !> any {
    final guard = magus\dir("internal/httpx");
    final mcp = magus\dir("internal/handler/mcp");
    final handlers = magus\layer("handler").dirs;
    final agent = figure\external("AI agents", look: figure\Look.plain);
    return figure\of("server-http", title: "The HTTP routes")
        .box(guard, label: "Guard", focal: true)
        .box(mcp, label: "/mcp")
        .group(figure\without(handlers, drop: [mcp]), label: "Console routes")
        .edgesFromGraph()
        .flowIn(agent, dst: guard)
        .scope(handlers + [guard]);
}
```

- `magus\dir(path)`: raises MGS7005 on directory graph does not hold, naming nearest.
- `magus\layer(name)`: raises MGS7006 on layer nothing declares.
- Typo fails figure at its line.
- `.diagram()`: lays out, raises every finding at once.
- `figure\draw(f, theme: figure\Theme.light)`: same, and paints.
- Prove figure draws before registering: call from scratch `main()` inside `try`/`catch`, run `magus buzz <file>`.

## Boxes, groups, actors

- `.box(dir, label:, sub:, tag:, focal:, look:, symbol:)`: one directory; `symbol:` anchors it at `magus\refs` result
- `.group(dirs, label:, ...)`: every directory in list of `magus\Dir` as one box
- `figure\external(name, sub:, tag:, link:, look:)`: actor: person, vendor, step, Buzz file, decision

Directories as records, never path strings:
- `magus\layer("handler").dirs`: every directory declared layer covers.
- `magus\dirs("internal/queue/*")`: every directory glob matches.
- `+` joins two lists.
- `figure\without(dirs, drop: [...])` leaves some out; raises when one it drops is not there.

Package joining layer joins group; figure needs no change.
Layers declared under `"layers"` in `magus\project`: workspace-relative directory or glob -> lowercase name.
Directory drawn once: box inside group's dirs refused -> drop it with `figure\without`.
Files of one package: one box, or actors when they are steps.

`look:` takes `figure\Look.plain`, `focal`, `store`, `external`, `input`, `optional` or `decision` (actors only, at most three exits).
Spend two accents on one point title makes.
Every look used gets `.legend(figure\Look.x, label:)` line.

## Edges

`.edgesFromGraph()`: draws every import and every declared `magus:calls` marker between two drawn boxes; required with two or more code boxes.
Arrows: importer -> imported.
Edge inside one group: not drawn.
Every drawn directory needs symbol index; unindexed one is finding, never empty edge list -> run `magus graph build` first.
- `.hideEdges(src, dst:, why:)`: drops graph edges from one dir list to another; reason required. Hide matching nothing is finding: code changed, look again.
- `.markEdge(src, dst:, label:, stroke:)`: labels or strokes one graph edge.
- `.flowIn(actor, dst: dir)`, `.flowOut(dir, dst: actor)`, `.flowAcross(actor, dst: actor)`: only hand edges. Each has actor at one end, claims flow nothing checks. Hand edge where graph already draws one: finding.

## Scope

`.scope(dirs)`: directories figure answers for; box or group must draw each one, or `.except(dirs, why:)` leaves it out.
`.except`: true exclusion only, not packages picture did not fit -> narrow scope instead.
Figure with no code behind it (decision process, CI arrangement): `.unscoped(why:)`.

## Findings

Nothing drawn until figure clean.
Do what finding says, at call it names.
Finding ending `layout bug, report it`: not yours to fix -> reorder declarations or split figure, and report it.

## Register, embed

Unregistered figure: checked by nothing.
- Find registry: `magus query diagrams`. magus's own tree: `docs/site/diagrams/all.buzz`: one import, one entry.
- `magus run diagrams-generate docs`: lays out every figure from symbol index -> light, dark, page SVGs, each with JSON receipt.
- Docs page embeds figure by id: `<!--diagram:<id>-->`.

Source can point back at figure: `magus:diagram <id>` comment beside code box depicts.
Marker whose figure is gone: finding.

## Console

Console Diagrams page draws workspace itself, no figure file: project graph, one project's targets, package import graph.
Each view through declared lens of scope, focus, depth.
- Needs running server (`magus server start`); import view needs symbol index.
- Server renders with same module -> lens over budget refused with own finding. Narrow lens; do not ask for bigger picture.

## Refused

- Coordinates, weights, pins: layout knob grows into second language. Shape with `rank`, `row`, `zone`, `boundary`, or split.
- Hand-drawn code edges, path strings: edge no code holds asserts what nobody checked; path string goes stale day package moves.

Buzz itself, syntax, strict mode: magus-buzz-lang.
````


</section>

<section class="landing-tabpanel">

```sh
magus agent install --tar | tar -xO -f - magus-diagram-full/SKILL.md
```

````markdown
# Drawing architecture figures with magus/figure

`magus/figure`, a Buzz module embedded in the binary, lays out an architecture figure
from the knowledge graph's records. A box is a directory the graph holds; a group is
a set of them. Edges between code boxes are the imports and declared calls those
records carry.

You never place anything or type an edge between two packages. The
module places the figure, or refuses with a finding naming the call to change.

## Contents

- When to reach for it
- The first figure
- Boxes, groups and actors
- Edges come from the graph
- Scope
- Findings name the call to change
- Register and embed
- The console
- What this skill refuses

## When to reach for it

Draw ONE claim per figure: a subsystem, a process, or a package scope, stated in the
title, with the boxes and connectors as its evidence. A figure that
needs more than the budgets below is two figures, an overview and a detail, each
its own `figure\of()`; the budgets are the design system's, and past them nobody
reads the picture.

- Use it when prose keeps restating how parts connect, or a doc page describes a
  request path or a pipeline. Use it when a reviewer needs to see which packages a
  change crosses.
- Never draw everything. A figure of the whole workspace blows every budget and
  proves nothing; the console already draws the whole project graph
  through a lens, so a hand-written copy of it is a second answer.
- Before writing one, look for a figure of the same scope with
  `magus query diagrams`; two figures of one subsystem drift apart.

Budgets: at most 9 boxes, 12 connectors, 3 zones, 2 accented elements, and 14
characters per connector label.

## The first figure

A figure is one exported function returning a `figure\Figure`. Both imports resolve
in any workspace; nothing needs a copy on disk:

```buzz
import "magus";
import "magus/figure";

export fun serverHttpFigure() > figure\Figure !> any {
    final guard = magus\dir("internal/httpx");
    final mcp = magus\dir("internal/handler/mcp");
    final handlers = magus\layer("handler").dirs;
    final agent = figure\external("AI agents", look: figure\Look.plain);
    return figure\of("server-http", title: "The HTTP routes")
        .box(guard, label: "Guard", focal: true)
        .box(mcp, label: "/mcp")
        .group(figure\without(handlers, drop: [mcp]), label: "Console routes")
        .edgesFromGraph()
        .flowIn(agent, dst: guard)
        .scope(handlers + [guard]);
}
```

- `magus\dir(path)` raises MGS7005 on a directory the graph does not hold, naming
  the nearest one. `magus\layer(name)` raises MGS7006 on a layer nothing declares.
  A typo fails the figure at the line that made it.
- `.diagram()` lays it out and raises every finding at once;
  `figure\draw(f, theme: figure\Theme.light)` does the same and paints it.
- Prove a figure draws before you register it: call it from a scratch `main()`
  inside `try`/`catch` and run `magus buzz <file>`.

`title` is the claim, `desc` the sentence under it, `eyebrow` the small label
above, all arguments of `figure\of`. `direction: figure\Direction.down` lays ranks top to
bottom instead of left to right.

## Boxes, groups and actors

| call | draws |
| --- | --- |
| `.box(dir, label:, sub:, tag:, focal:, look:, symbol:)` | one directory; `symbol:` anchors it at a `magus\refs` result |
| `.group(dirs, label:, ...)` | every directory in a list of `magus\Dir` as one box |
| `figure\external(name, sub:, tag:, link:, look:)` | an actor: a person, a vendor, a step, a Buzz file, a decision |

Take directories as records, never as path strings:

- `magus\layer("handler").dirs` is every directory a declared layer covers.
- `magus\dirs("internal/queue/*")` is every directory a glob matches.
- `+` joins two lists. `figure\without(dirs, drop: [...])` leaves some out, and
  raises when one it drops is not there.

A package that joins the layer joins the group, and the figure needs no change.
Layers are declared under `"layers"` in `magus\project`: a workspace-relative
directory or glob to a lowercase name.

A directory is drawn once: a box inside a group's dirs is refused, so drop it with
`figure\without`. Files of one package are one box, or actors when they are steps.

`look:` takes `figure\Look.plain`, `focal`, `store`, `external`, `input`, `optional` or
`decision` (actors only, at most three exits). Spend the two accents on the one point
the title makes. Give every look you use a `.legend(figure\Look.x, label:)` line.

## Edges come from the graph

`.edgesFromGraph()` draws every import and every declared `magus:calls` marker
between two drawn boxes; two or more code boxes require it. Arrows point importer to
imported. An edge inside one group is not drawn. Every drawn directory needs a
symbol index. An unindexed one is a finding, never an empty edge list, so run
`magus graph build` first.

- `.hideEdges(src, dst:, why:)` drops graph edges from one list of dirs to another,
  reason required. A hide that matches nothing is a finding: the code changed, so
  look again.
- `.markEdge(src, dst:, label:, stroke:)` labels or strokes one graph edge.
- `.flowIn(actor, dst: dir)`, `.flowOut(dir, dst: actor)` and `.flowAcross(actor, dst:
  actor)` are the only hand edges. Each has an actor at one end and claims flow,
  which nothing checks. A hand edge where the graph already draws one is a finding.

A relationship between two packages that no import holds, a socket or an HTTP
call, is declared in the source with a `magus:calls <dir> <transport>` marker, never drawn
by hand. The marker becomes a calls edge `edgesFromGraph()` draws.

## Scope

`.scope(dirs)` names the directories the figure answers for. A box or a group must
draw each one, or `.except(dirs, why:)` leaves it out. `.except` is for a true
exclusion, not a list of packages the picture did not fit: narrow the scope
instead. A figure with no code behind it (a decision process, a CI arrangement) says
so with `.unscoped(why:)`.

## Findings name the call to change

Nothing is drawn until the figure is clean. Real ones:

```text
figure "server-http": "internal/server" is in scope("internal/**") but nothing draws it; add it to a box or a group, or except([...], why:)
figure "t": draws 2 boxes of code and no edge between them; call edgesFromGraph()
figure "t": "a" has no symbol index, so its imports are unknown and edgesFromGraph() cannot draw them; build it with `magus graph build`
```

Do what the finding says, at the call it names. A finding ending `layout bug, report
it` is not yours to fix: reorder declarations or split the figure, and report it.

## Register and embed

A figure nobody registers is checked by nothing.

- Find the workspace's registry with `magus query diagrams`. In magus's own tree
  it is `docs/site/diagrams/all.buzz`: one import plus one entry.
- `magus run diagrams-generate docs` lays every figure out from the symbol index.
  It writes the light, dark and page SVGs, each with a JSON receipt: the
  figure id, the stamp, the claim counts and the findings; no index digest.
- A docs page embeds a figure by id with `<!--diagram:<id>-->`.

Source code can point back at a figure with a `magus:diagram <id>` comment beside the
code a box depicts. A marker whose figure is gone is a finding.

## The console

The console's Diagrams page draws the workspace itself, with no figure file. It shows
the project graph, one project's targets, and the package import graph. Each view
goes through a declared lens of scope, focus and depth.

- It needs a running server (`magus server start`); the import view needs the
  symbol index.
- The server renders with the same module, so a lens over the budget is refused
  with its own finding. Narrow the lens; do not ask for a bigger picture.

## What this skill refuses

- **Coordinates, weights, pins.** A layout knob grows into a second language. Shape
  a figure with `rank`, `row`, `zone` and `boundary`, or split it.
- **Hand-drawn code edges and path strings.** An edge no code holds asserts
  something nobody checked, and a path string goes stale the day a package moves.

Writing the Buzz itself, the syntax and strict mode: magus-buzz-lang.
````


</section>

</article>
