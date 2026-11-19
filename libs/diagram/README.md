# libs/diagram

`magus/diagram` draws an architecture diagram from the knowledge graph's own records.
A box takes a `magus\Dir`, a group takes a list of them, and the edges come from the
imports and declared calls those records carry. The binary embeds the module through
`std/lib.go`, so a diagram imports it by path and needs no checkout of this directory.

```buzz
import "magus";
import "magus/diagram";

export fun serverHttp() > Diagram !> any {
    final guard = magus\graph().dir("internal/httpx");
    final mcp = magus\graph().dir("internal/handler/mcp");
    return diagram\of("server-http", title: "The HTTP routes")
        .box(guard, label: "Guard", focal: true)
        .box(mcp, label: "/mcp")
        .group(diagram\without(magus\graph().layer("handler").dirs, drop: [mcp]), label: "Connect RPC")
        .edgesFromGraph();
}
```

## API

| Call                                   | What it does                                                     |
| -------------------------------------- | ---------------------------------------------------------------- |
| `of(id, title:, eyebrow:, desc:, ...)` | Starts a diagram; `direction:` and `level:` set the rest.         |
| `without(dirs, drop:)`                 | `dirs` less `drop`; raises when `drop` names one `dirs` lacks.   |
| `person(name, ...)`                    | An `Actor` for someone outside the code; joins any level.        |
| `external(name, ...)`                  | An `Actor` for a service or file outside the code; any level.    |
| `step(name, ...)`                      | An `Actor` for a process step; only in a `Level.process` diagram. |
| `stage(dir, refs:, ...)`               | An `Actor` for a symbol directly in `dir`; `Level.symbol`.       |
| `.box(dir, ...)`                       | One directory; `symbol:` takes a `magus\graph().refs` result.            |
| `.group(dirs, label:, ...)`            | Every directory in `dirs` as one box.                            |
| `.actor(a)`                            | Draws an actor at this point in declaration order.               |
| `.scope(dirs)`                         | Directories the diagram answers for.                              |
| `.except(dirs, why:)`                  | Directories in scope it leaves out, and why.                     |
| `.edgesFromGraph()`                    | Draws imports and declared calls between drawn boxes.            |
| `.hideEdges(src, dst:, why:)`          | Hides graph edges from one list of dirs to another.              |
| `.markEdge(src, dst:, ...)`            | Labels or strokes one graph edge.                                |
| `.flowIn`, `.flowOut`, `.flowAcross`   | Hand edges; each has an actor at one end.                        |
| `.zone`, `.boundary`, `.rank`, `.row`  | Bands over dirs and actors; an edge inside a rank lies flat.     |
| `.legend(look, label:)`                | One legend entry.                                                |
| `draw(f, theme:, anchorHref:)`         | Lay out and paint with `Theme.page`, `.light` or `.dark`.        |
| `.layout()`                            | Lay out only, for a receipt of the placed boxes and edges.       |
| `atlas(diagrams, column:)`              | Reads diagrams together: drill-down links and a width budget.     |
| `drawLayout(d, theme:, drillHref:)`    | Paints a laid-out diagram; `{id}` takes a box's drill target.     |
| `claims(d)`, `drillLinks(d)`           | A laid-out diagram's edges by claim, and its boxes' drill links.  |

## A diagram is data

`Diagram` is a plain record: `id`, `title`, `eyebrow`, `desc`, `direction`, `level`,
`unscopedWhy` (empty for a diagram that draws code), `graphEdges`, and the lists `boxes`,
`scopes`, `exclusions`, `hiddenEdges`, `edgeMarks`, `flows`, `zones`, `alignments` and
`legends`. Their records are `Box`, `Exclusion`, `HiddenEdges`, `EdgeMark`, `Flow` (two
`End`s, each a directory or an actor), `Zone`, `Alignment` and `Legend`; every set of
directories is a plain `[magus\Dir]`. A look, stroke,
direction, axis, level or actor kind is always its enum (`Look`, `Stroke`, `Direction`,
`Axis`, `Level`, `ActorKind`), never a string. A host's actor with no kind is a step: a host
draws its own units, projects, targets or jobs, as a process.

The builder methods only fill those fields, so a `Diagram{...}` literal holding the same
fields draws the same bytes. Every check runs in `draw()` and `layout()`.

A host builds the record from its own data and calls `draw` with no Buzz source of its own:
`diagram.Draw` in `embed.go` takes the Go mirror of the record, sends each enum as its case's
name, and refuses a name no case holds. The server's Diagrams handler and the browser
playground's `buzz.drawDiagram` both go through it. `TestMirrorMatchesTheModule` fails when the
Go mirror drifts from the records declared here.

## Rules the module enforces

- A diagram draws at one level: process, project, package or symbol. A box of another
  level is refused, naming the diagram it belongs in. The root `.` is the root project,
  never a package. A `Level.symbol` diagram's stages all sit in the one package it scopes.
- An actor says what it stands for: an `Actor` with no kind is refused.
- At most 9 boxes and 12 edges, refused with the narrower scope to draw apart, and a
  description of at most 60 words.
- A directory is drawn once. A box inside a group's dirs is refused; drop it with
  `without(dirs, drop: [...])`.
- Every directory a `scope()` names is drawn or excepted. A package that joins a layer
  joins the group built from that layer, and nothing else in the diagram changes.
- Two or more code boxes need `edgesFromGraph()`, and every drawn directory needs a
  symbol index. An unindexed directory is a finding, never an empty edge list.
- A code edge is never drawn by hand. The hand edges each need an actor, and they claim
  `flow`, which nothing checks.

Declarations never raise. `draw()` and `layout()` raise every finding at once, one per
line, each naming the call to change and naming a list of dirs by the deepest directory
holding them all (`scope("internal/handler/**")`), never by every path. `without` raises
immediately when handed something that is not a record.

## Tests

The module takes no host module. Its tests build `Dir`, `Layer` and `RefsResult` records
as map literals, the shape the host returns, and run with
`magus run test libs/diagram`.
