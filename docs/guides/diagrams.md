---
title: Writing diagrams
description: How to write an architecture figure with magus/figure, from the knowledge graph's own directory and layer records, and keep it true.
tags: [diagrams, figure, buzz, docs, drift]
---

# Writing diagrams

Every architecture figure on this site is a Buzz file in `docs/site/diagrams/`,
drawn by `magus/figure`, a module embedded in the magus binary. A box is a
directory the knowledge graph holds, a group is a set of them, and the edges
between code boxes are the imports and declared calls those directories carry.
You declare what the figure shows; the module places everything and refuses a
figure it cannot draw, naming the call to change.

<!--diagram:diagram-pipeline-->

## What a figure is

A figure makes one claim: the title states it, and the boxes and connectors are
the evidence. There are hard budgets:
at most 9 boxes, 12 connectors, 3 zones and 2 accented elements. A figure that
needs more is two figures, an overview and a detail, each with its own
`figure\of()`.

## The first figure

```buzz
namespace cacheRead;

import "magus";
import "magus/figure";

export fun cacheReadFigure() > figure\Figure !> any {
    final cache = magus\dir("internal/cache");
    final run = figure\external("Run target", look: figure\Look.plain);
    return figure\of("cache-read", title: "A hit skips the run")
        .box(cache, label: "Cache key", focal: true)
        .flowOut(cache, dst: run, label: "miss")
        .scope([cache]);
}
```

`magus\dir` raises MGS7005 when the graph holds no such directory, naming the
nearest one, so a typo fails at the line that made it. Register the figure in
`docs/site/diagrams/all.buzz` (the import and an `allFigures()` entry), then
embed it on a page with `<!--diagram:cache-read-->`.

## Boxes, groups and actors

- `box(dir, label:, sub:, focal:, look:)` draws one directory.
- `group(dirs, label:)` draws every directory in a list as one box. Take the
  directories as records: `magus\layer("handler").dirs` is every directory a
  declared layer covers, `magus\dirs("internal/queue/*")` every directory a
  glob matches. `+` joins two lists, and `figure\without(dirs, drop: [...])`
  leaves some out. A package added to the layer joins the group with no edit to
  the figure.
- `figure\external(name, ...)` is an actor: a person, a vendor service, a step,
  a Buzz file or a decision.

The layers this repository declares live under `"layers"` in the root
`magusfile.buzz`: transport, handler, service, repository and composition.

A directory is drawn once, so a box inside a group's dirs is refused; drop it
with `figure\without`. `look:` takes `figure\Look.focal`, `store`, `external`,
`input`, `optional`, `decision` (actors only) or `plain`. Add a `legend()` line
for each look you use.

## Edges

`edgesFromGraph()` draws every import and every declared `magus:calls` marker
between two drawn boxes, and a figure with two or more code boxes must call it.
Every drawn directory needs a symbol index, so run `magus graph build` first.

- `hideEdges(src, dst:, why:)` drops graph edges from one list of dirs to another, with
  a reason. A hide that matches nothing is a finding.
- `markEdge(src, dst:, label:, stroke:)` labels or strokes one graph edge.
- `flowIn`, `flowOut` and `flowAcross` are the only hand edges, and each has an
  actor at one end. An edge between two packages is never drawn by hand.

## Scope

`scope(dirs)` names the directories the figure answers for. Every one must be
drawn by a box or a group, or left out with `except(dirs, why:)`, which is for a
true exclusion. A figure with no code behind it says so with `unscoped(why:)`.

## Findings

Nothing is drawn until the figure is clean. Each finding names the call to
change:

- `10 nodes exceeds the budget of 9; split into overview plus detail, one figure\of() each`:
  cut or fold boxes, or make two figures.
- `"internal/x" is in scope("internal/**") but nothing draws it`: draw it, or
  narrow the scope. A finding names a list of dirs by the deepest directory
  holding them all, or by its one path.
- `draws 2 boxes of code and no edge between them; call edgesFromGraph()`.
- `"a" has no symbol index`: build it with `magus graph build`.
- `hideEdges(...) hides no edge the graph draws`: the code changed; drop the
  hide.

## Generating the files

`magus run diagrams-generate docs` lays every figure out from the symbol index
and writes into `docs/assets/gen/` the dark and light pair the README loads,
the page copy the site inlines, and a receipt beside each: the stamp, the claim
counts, the findings and the index digest it was drawn at. The stamp is also in
each SVG's metadata. It fails when the committed files move; `:rw` rewrites
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
