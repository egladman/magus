# Drawing architecture figures with magus/figure

`magus/figure` is a Buzz module embedded in the binary that lays out an architecture
figure from the knowledge graph's own records. A box is a directory the graph holds, a
group is a set of them, and the edges between code boxes are the imports and declared
calls those records carry. You never place anything and never type an edge between two
packages. The module places the figure, and refuses one it cannot draw cleanly with a
finding that names the call to change.

## When to reach for it

Draw ONE claim per figure: a subsystem, a process, or a package scope, stated in
the title, with the boxes and connectors as its evidence.{{if .Full}} A figure that
needs more than the budgets below is two figures, an overview and a detail, each
its own `figure\of()`; the budgets are the design system's, and past them nobody
reads the picture.{{end}}

- Reach for it when prose keeps restating how parts connect, when a doc page
  describes a request path or a pipeline, or when a reviewer needs to see which
  packages a change crosses.
- Never draw everything. A figure of the whole workspace blows every budget and
  proves nothing{{if .Full}}; the console already draws the whole project graph
  through a lens, so a hand-written copy of it is a second answer{{end}}.
- Before writing one, look for an existing figure of the same scope with
  `{{cmd "query"}} diagrams`{{if .Full}}; two figures of one subsystem drift apart{{end}}.

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
    final handlers = figure\layerSet(magus\layer("handler"));
    final agent = figure\external("AI agents", look: figure\Look.plain);
    return figure\of("server-http", title: "The HTTP surface")
        .box(guard, label: "Guard", focal: true)
        .box(mcp, label: "/mcp")
        .group(handlers.without([mcp]), label: "Console routes")
        .edgesFromGraph()
        .flowIn(agent, dst: guard)
        .scope(handlers.plus([guard]));
}
```

`magus\dir(path)` raises MGS7005 on a directory the graph does not hold, naming the
nearest one, and `magus\layer(name)` raises MGS7006 on a layer nothing declares. A typo
fails the figure at the line that made it. `.diagram()` lays it out and raises every
finding at once; `figure\draw(f, theme: figure\Theme.light)` does the same and paints
it. Prove a figure draws before you register it: call it from a scratch `main()` inside
`try`/`catch` and run it with `{{cmd "buzz"}} <file>`.

{{if .Full}}`title` is the claim, `desc` the sentence under it, `eyebrow` the small label
above, all arguments of `figure\of`. `direction: figure\Direction.down` lays ranks top to
bottom instead of left to right.

{{end}}## Boxes, groups and actors

| call | draws |
| --- | --- |
| `.box(dir, label:, sub:, tag:, focal:, look:, symbol:)` | one directory; `symbol:` anchors it at a `magus\refs` result |
| `.group(set, label:, ...)` | every directory in a `DirSet` as one box |
| `figure\external(name, sub:, tag:, link:, look:)` | an actor: a person, a vendor, a step, a Buzz file, a decision |

Build a set from records, never from a list of path strings: `figure\layerSet(magus\layer("handler"))`
takes every directory a declared layer covers, `figure\setOf(magus\dirs("internal/queue/*"))`
every directory a glob matches, and `.without([...])` and `.plus([...])` derive one.
A package that joins the layer joins the group, and nothing in the figure changes.
Layers are declared under `"layers"` in `magus\project`, workspace-relative directory or
glob to a lowercase name.

A directory is drawn once: a box inside a group's set is refused, so drop it with
`.without([...])`. Files of one package are one box, or actors when they are steps.

`look:` takes `figure\Look.plain`, `focal`, `store`, `external`, `input`, `optional` or
`decision` (actors only, at most three exits). Spend the two accents on the one point the
title makes, and give every look you use a `.legend(figure\Look.x, label:)` line.

## Edges come from the graph

`.edgesFromGraph()` draws every import, and every declared `magus:calls` marker, between
two drawn boxes; two or more code boxes require it. Arrows point importer to imported. An
edge inside one group is not drawn. Every drawn directory needs a symbol index: an
unindexed one is a finding, never an empty edge list, so run `{{cmd "graph build"}}` first.

- `.hideEdges(src, dst:, why:)` drops graph edges from one set to another, reason
  required. A hide that matches nothing is a finding: the code changed, so look again.
- `.markEdge(src, dst:, label:, stroke:)` labels or strokes one graph edge.
- `.flowIn(actor, dst: dir)`, `.flowOut(dir, dst: actor)` and `.flowAcross(actor, dst:
  actor)` are the only hand edges. Each has an actor at one end and claims flow, which
  nothing checks. A hand edge where the graph already draws one is a finding.

{{if .Full}}A relationship between two packages that no import holds, a socket or an HTTP
call, is declared in the source with a `magus:calls <dir> <transport>` marker, never drawn
by hand. The marker becomes a calls edge `edgesFromGraph()` draws.

{{end}}## Scope

`.scope(set)` names the directories the figure answers for. Every one must be drawn by a
box or a group, or left out with `.except(set, why:)`, which is for a true exclusion, not a
list of packages the picture did not fit. Narrow the scope instead. A figure with no code
behind it, a decision process or a CI arrangement, says so with `.unscoped(why:)`.

## Findings name the call to change

Nothing is drawn until the figure is clean. Real ones:

```text
figure "server-http": "internal/server" is in scope(...) but nothing draws it; add it to a box or a group, or except(setOf([...]), why:)
figure "t": draws 2 boxes of code and no edge between them; call edgesFromGraph()
figure "t": "a" has no symbol index, so its imports are unknown and edgesFromGraph() cannot draw them; build it with `magus graph build`
```

Do what the finding says, at the call it names. A finding ending `layout bug, report it`
is not yours to fix: reorder declarations or split the figure, and report it.

## Register and embed

A figure nobody registers is checked by nothing. Find the workspace's registry with
`{{cmd "query"}} diagrams`; in magus's own tree it is `docs/site/diagrams/all.buzz`, one
import plus one entry. `{{cmd "run"}} diagrams-generate docs` lays every figure out from the
symbol index and writes the light, dark and page SVGs with a JSON receipt beside each:
the stamp, the claim counts, the findings, and the index digest it was drawn at. A docs
page embeds a figure by id with `<!--diagram:<id>-->`.

Source code can point back at a figure with a `magus:diagram <id>` comment beside the
code a box depicts; a marker whose figure is gone is a finding.

## The console

The console's Diagrams page draws the workspace itself, with no figure file: the
project graph, one project's targets, and the package import graph, each through a
declared lens of scope, focus and depth. It needs a running server
(`{{cmd "server start"}}`), and the import view needs the symbol index. The server
renders with the same module, so a lens over the budget is refused with its own
finding: narrow the lens rather than asking for a bigger picture.

## What this skill refuses

- **Coordinates, weights, pins.** A layout knob grows into a second language. Shape a
  figure with `rank`, `row`, `zone` and `boundary`, or split it.
- **Hand-drawn code edges and path strings.** An edge nobody's code holds asserts
  something nobody checked, and a path string goes stale the day a package moves.

Writing the Buzz itself, the syntax and the strict-mode rules: {{skill "buzz-lang"}}.
