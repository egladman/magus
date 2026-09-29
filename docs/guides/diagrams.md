---
title: Writing diagrams
description: How to write an architecture figure with flow, compose it over the observed import graph, and keep it true with the drift check.
tags: [diagrams, flow, buzz, docs, drift]
---

# Writing diagrams

Every architecture figure on this site is a Buzz file in `docs/site/diagrams/`,
laid out by `flow` (`libs/diagram/flow.buzz`) and painted by the renderer beside
it. You declare what the figure shows and what each connector claims about the
code; flow places everything, and the drift check fails the build when the code
moves out from under the picture.

<!--diagram:diagram-pipeline-->

## What a figure is

A figure makes one claim: the title states it, and the boxes and connectors are
the evidence. There are hard budgets:
at most 9 boxes, 12 connectors, 3 zones and 2 accented elements. A figure that
needs more is two figures, an overview and a detail, each with its own `flow()`.

## The twelve-line first figure

```buzz
namespace cacheRead;
import "../libs/diagram/diagram" as _;
import "../libs/diagram/flow" as _;
export fun cacheReadDiagram() > Diagram !> str {
    return flow("cache-read")
        .title("A hit skips the run")
        .node("key", label: "Cache key", anchor: "internal/cache")
        .node("run", label: "Run target")
        .edge("key", dst: "run", label: "miss")
        .scope(["internal/cache"])
        .diagram();
}
```

Register it in `docs/site/diagrams/all.buzz` (the import and an `allDiagrams()`
entry), then embed it on a page with `<!--diagram:cache-read-->`.
`magus run diagrams-generate docs` checks it and writes the committed light and
dark SVG pair to `docs/assets/gen/`.

## Roles and claims

A box's `role:` picks its treatment: `Role.focal` takes the accent, `store` is
something persistent, `external` sits outside what the figure depicts, `input`
is data arriving from outside, `optional` is dashed, and `decision` is a diamond
with at most three exits. Add a `legend()` line for each role you use.

Every connector claims what it asserts about the code:

- `Claim.imports`: the source package imports the destination. Checked against
  the import graph.
- `Claim.calls`: a call across a process or network boundary, such as a hook
  running magus or the console fetching an endpoint. Declared and counted; no
  index can see it.
- `Claim.flow`: narrative, the default. Never checked, but counted, so an
  all-flow figure reads as unverified.

## Scope and omits

`scope([...])` names the directories the figure depicts, a whole-segment glob
per entry (`internal/handler/*`, `internal/graph/**`). Every Go package under
scope must be anchored by a box, or left out with `omit(path, why:)`; the
reason is required. A figure with no code behind it says so with
`unscoped(why:)` instead.

`anchor:` is a path in this repository, a package or a file; a file anchor
covers its package. Anything outside the repository takes `link:`.

## Composing over an observed set

`magus run diagrams-observe docs` reads the symbol index (build it with
`magus graph build`) and writes `docs/site/diagrams/gen/<id>.buzz`: every package
under the figure's scope and the imports between them, stamped. Import it
aliased and pass it to `flow`:

```buzz
return flow("guard-path", observed: guardPathGen\guardPathObserved())
```

A declared box with an observed box's id or anchor merges with it, and your
label, role and sub win. Observed boxes you do not declare are drawn as they
are unless omitted. Hide an observed import with `omitEdge(src, dst:, why:)`,
reason required. A declared connector over an observed import must claim
`Claim.imports`.

Register the observed half in `all.buzz` too: the aliased gen/ import and an
`observedFor` entry.

## rank, row and order

flow takes no coordinates and no weights. Three declarations relate boxes
instead: `rank([...])` puts boxes in one column, `row([...])` lines boxes up on
one midline so the connectors between them run straight, and `order([...])`
sorts boxes within their shared column. Zones stack as bands in declaration
order. A constraint the layout cannot honor is refused with a finding, never
ignored.

## Findings

Nothing is drawn until the figure is clean. Each finding names the call to
change:

- `10 nodes exceeds the budget of 9; split into overview plus detail`: cut or
  fold boxes, or make two figures.
- `label "..." runs past 14 characters`: shorten it or move the words to
  `desc()`.
- `names no code; add scope([...]) or unscoped(why:)`: say what the figure
  depicts.
- `internal/x is under scope ... but no node anchors it`: anchor it or omit it
  with a reason.
- `edge a -> b replaces an observed import but claims flow`: claim
  `Claim.imports` or hide it with `omitEdge`.
- `omitEdge "a" -> "b" names no observed edge`: the code changed; drop the
  override.
- `rank([...]) contradicts the path "a" -> "b"`: a path already orders them;
  drop one from the rank.
- `observed set is stale`: rerun `magus run diagrams-observe docs`.

## The drift check

`magus run diagrams-generate docs` verifies every figure before it writes a
byte: anchors exist, every package under scope is anchored or omitted, every
omit names a real path. Add `-- --verify-imports` after `magus graph build` and
it also checks each imports claim and each observed stamp against the index. A
summary line per figure counts its anchors and claims.

## The console view

The console's Diagrams page draws the project graph, one project's targets and
the import graph from `/api/v1/diagrams`, each through a lens of scope, focus
and depth. The server embeds the same flow source this site builds with, and
anchored boxes link to their source.

## Credit

The design system, its palette, role treatments and budgets, is
[cathrynlavery/diagram-design](https://github.com/cathrynlavery/diagram-design)
(MIT).
