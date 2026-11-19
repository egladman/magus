---
title: Writing diagrams
description: How to write an architecture diagram with magus/diagram, from the knowledge graph's own directory and layer records, and keep it true.
tags: [diagrams, diagram, buzz, docs, drift]
---

# Writing diagrams

Every architecture diagram on this site is a Buzz file in `docs/site/diagrams/`,
drawn by `magus/diagram`, a module embedded in the magus binary. A box is a
directory the knowledge graph holds, a group is a set of them, and the edges
between code boxes are the imports and declared calls those directories carry.
You declare what the diagram shows; the module places everything and refuses a
diagram it cannot draw, naming the call to change.

<!--diagram:diagram-pipeline-->

## What a diagram is

A diagram makes one claim: the title states it, and the boxes and connectors are
the evidence. It draws at one level, so it cannot grow too detailed:

| Level | Its boxes |
| --- | --- |
| `diagram\Level.process` | steps, `diagram\step()` |
| `diagram\Level.project` | projects, `box(dir)` |
| `diagram\Level.package` | packages, `box(dir)` and `group(dirs)` |
| `diagram\Level.symbol` | the symbols inside one package, `diagram\stage()` |

`diagram\of(id, level:)` declares it; left out, the boxes decide it. A box of
another level is refused with the diagram it belongs in, and a person or an
external joins any level. `diagram\Level.file` has no unit constructor yet and is
refused.

Past the level there are hard budgets: at most 9 boxes, 12 connectors, 3 zones,
2 accented elements and a 60-word description, which is the alt text. A diagram
that needs more is two diagrams, an overview and a detail, each with its own
`diagram\of()`. The docs registry also refuses a diagram wider than its 1200-unit
column.

## The first diagram

```buzz
namespace cacheRead;

import "magus";
import "magus/diagram";

export fun cacheReadDiagram() > diagram\Diagram !> any {
    final cache = magus\graph().dir("internal/cache");
    final run = diagram\external("CI runner", look: diagram\Look.plain);
    return diagram\of("cache-read", title: "A hit skips the run", level: diagram\Level.package)
        .box(cache, label: "Cache key", focal: true)
        .flowOut(cache, dst: run, label: "miss")
        .scope([cache]);
}
```

`magus\graph().dir` raises MGS7005 when the graph holds no such directory, naming the
nearest one, so a typo fails at the line that made it. Register the diagram in
`docs/site/diagrams/all.buzz` (the import and an `allDiagrams()` entry), then
embed it on a page with `<!--diagram:cache-read-->`.

## Boxes, groups and actors

- `box(dir, label:, sub:, focal:, look:)` draws one directory.
- `group(dirs, label:)` draws every directory in a list as one box. Take the
  directories as records: `magus\graph().layer("handler").dirs` is every directory a
  declared layer covers, `magus\graph().dirs("internal/queue/*")` every directory a
  glob matches. `+` joins two lists, and `diagram\without(dirs, drop: [...])`
  leaves some out. A package added to the layer joins the group with no edit to
  the diagram.
- An actor is built by what it stands for, and an `Actor` record with no kind is
  refused:
  - `diagram\person(name, ...)`: someone outside the code.
  - `diagram\external(name, ...)`: a vendor service, a host, a file a person writes.
  - `diagram\step(name, ...)`: a step of a process, only in a `Level.process` diagram.
  - `diagram\stage(dir, refs:, name:, ...)`: a step inside one package, carried out by
    the symbol `magus\graph().refs` names. Its definition must sit directly in `dir`,
    and the stage is anchored there. Stages draw a `Level.symbol` diagram scoped to
    that one package.

The layers this repository declares live under `"layers"` in the root
`magusfile.buzz`: transport, handler, service, repository and composition.

A directory is drawn once, so a box inside a group's dirs is refused; drop it
with `diagram\without`. `look:` takes `diagram\Look.focal`, `store`, `external`,
`input`, `optional`, `decision` (actors only) or `plain`. Add a `legend()` line
for each look you use.

## Edges

`edgesFromGraph()` draws every import and every declared `magus:calls` marker
between two drawn boxes, and a diagram with two or more code boxes must call it.
Every drawn directory needs a symbol index, so run `magus graph build` first.

- `hideEdges(src, dst:, why:)` drops graph edges from one list of dirs to another, with
  a reason. A hide that matches nothing is a finding.
- `markEdge(src, dst:, label:, stroke:)` labels or strokes one graph edge.
- `flowIn`, `flowOut` and `flowAcross` are the only hand edges, and each has an
  actor at one end. An edge between two packages is never drawn by hand.

## Scope

`scope(dirs)` names the directories the diagram answers for. Every one must be
drawn by a box or a group, or left out with `except(dirs, why:)`, which is for a
true exclusion. A diagram with no code behind it says so with `unscoped(why:)`.

To find the directories a question touches, ask the graph before writing the
scope: `magus query 'kind=~^(dir|project|package)$ how does a target reach the cache'`.
To check whether a diagram already answers it, ask for diagrams:
`magus query 'kind=diagram how does the Buzz engine load a spell'`. The graph
reads each diagram's id, title, eyebrow and description from its `diagram\of`
call, and links the diagram to every directory a `magus\graph().dir` call in the same
function names. The query only suggests. A diagram draws what its source names,
so rendering never reads the ranking and the same source draws the same diagram.

## Findings

Nothing is drawn until the diagram is clean. Each finding names the call to
change:

- `stage("Parser") is a symbol inside "libs/lang", and this diagram is Level.package;
  draw the stages in a Level.symbol diagram scoped to "libs/lang", and box that
  package here`: split the diagram by level.
- `box(".") is the root project, and this diagram is Level.package`: box the
  packages the claim needs.
- `10 nodes exceeds the budget of 9; split into overview plus detail, one diagram\of() each: scope a detail diagram to "internal/handler/**", the widest group, ...`:
  the remedy names the narrower scope to draw apart.
- `"internal/x" is in scope("internal/**") but nothing draws it`: draw it, or
  narrow the scope. A finding names a list of dirs by the deepest directory
  holding them all, or by its one path.
- `draws 2 boxes of code and no edge between them; call edgesFromGraph()`.
- `"a" has no symbol index`: build it with `magus graph build`.
- `hideEdges(...) hides no edge the graph draws`: the code changed; drop the
  hide.

## Drill-down

`diagram\atlas(diagrams, column:)` reads a registry's diagrams together. Its
`diagram(f)` links each box to the diagram one level down that draws it: a package
box to the `Level.symbol` diagram of that package, a group to the `Level.package`
diagram that boxes each of its directories. The page copy paints the link, and
the receipt beside each diagram lists the groups no diagram draws under
`undrilled`.

## Generating the files

`magus run diagrams-generate docs` lays every diagram out from the symbol index
and writes into `docs/assets/gen/` the dark and light pair the README loads,
the page copy the site inlines, and a receipt beside each: the stamp, the claim
counts (imports, calls, flow) and the findings. The receipt carries no index
digest, so it changes only when its diagram does. A box anchoring a symbol stamps
the symbol and its signature rather than where it is defined: moving the
definition changes nothing, and a signature change moves the stamp. The stamp is
also in each SVG's metadata. It fails when the committed files move; `:rw` rewrites
them. The site render reads only the page copies, so it never needs the index.

## The console view

The console's Diagrams page draws the project graph, one project's targets and
the import graph from `/api/v1/diagrams`, each through a lens of scope, focus
and depth. The daemon embeds the same module this site builds with, and boxes
link to their source.

## Credit

The design system, its palette, role treatments and budgets, is
[cathrynlavery/diagram-design](https://github.com/cathrynlavery/diagram-design)
(MIT).
