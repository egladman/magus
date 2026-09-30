# libs/figure

`magus/figure` draws an architecture figure from the knowledge graph's own records.
A box takes a `magus\Dir`, a group takes a set of them, and the edges come from the
imports and declared calls those records carry. The binary embeds the module through
`std/lib.go`, so a figure imports it by path and needs no checkout of this directory.

```buzz
import "magus";
import "magus/figure";

export fun serverHttp() > Figure !> any {
    final guard = magus\dir("internal/httpx");
    final mcp = magus\dir("internal/handler/mcp");
    return figure\of("server-http", title: "The HTTP surface")
        .box(guard, label: "Guard", focal: true)
        .box(mcp, label: "/mcp")
        .group(figure\layerSet(magus\layer("handler")).without([mcp]), label: "Connect RPC")
        .edgesFromGraph();
}
```

## Surface

| Call                                   | What it does                                                     |
| -------------------------------------- | ---------------------------------------------------------------- |
| `of(id, title:, eyebrow:, desc:, ...)` | Starts a figure; `direction:` and `generated:` set the rest.     |
| `setOf(dirs)`, `layerSet(layer)`       | Build a `DirSet`; `.without(dirs)` and `.plus(dirs)` derive one. |
| `external(name, ...)`                  | An `Actor`: a box that names no directory.                       |
| `.box(dir, ...)`                       | One directory; `symbol:` takes a `magus\refs` result.            |
| `.group(set, label:, ...)`             | Every directory in the set as one box.                           |
| `.actor(a)`                            | Draws an actor at this point in declaration order.               |
| `.scope(set)`                          | Directories the figure answers for.                              |
| `.except(set, why:)`                   | Directories in scope it leaves out, and why.                     |
| `.edgesFromGraph()`                    | Draws imports and declared calls between drawn boxes.            |
| `.hideEdges(src, dst:, why:)`          | Hides graph edges from one set to another.                       |
| `.markEdge(src, dst:, ...)`            | Labels or strokes one graph edge.                                |
| `.flowIn`, `.flowOut`, `.flowAcross`   | Hand edges; each has an actor at one end.                        |
| `.zone`, `.boundary`, `.rank`, `.row`  | Layout bands over sets and actors.                               |
| `.legend(look, label:)`                | One legend entry.                                                |
| `draw(f, theme:, anchorHref:)`         | Lay out and paint with `Theme.page`, `.light` or `.dark`.        |
| `.diagram()`                           | Lay out only, for a receipt of the placed boxes and edges.       |

## A figure is data

`Figure` is a plain record: `id`, `title`, `eyebrow`, `desc`, `direction`, `generated`,
`unscopedWhy` (empty for a figure that draws code), `graphEdges`, and the lists `boxes`,
`scopes`, `exclusions`, `hiddenEdges`, `edgeMarks`, `flows`, `zones`, `alignments` and
`legends`. Their records are `Box`, `DirSet`, `Exclusion`, `HiddenEdges`, `EdgeMark`, `Flow`
(two `End`s, each a directory or an actor), `Zone`, `Alignment` and `Legend`. A look, stroke,
direction or axis is always its enum (`Look`, `Stroke`, `Direction`, `Axis`), never a string.

The builder methods only fill those fields, so a `Figure{...}` literal holding the same
fields draws the same bytes. Every check runs in `draw()` and `diagram()`.

A host builds the record from its own data and calls `draw` with no Buzz source of its own:
`figure.Draw` in `embed.go` takes the Go mirror of the record, sends each enum as its case's
name, and refuses a name no case holds. The server's Diagrams handler and the browser
playground's `buzz.drawFigure` both go through it. `TestMirrorMatchesTheModule` fails when the
Go mirror drifts from the records declared here.

## Rules the module enforces

- A directory is drawn once. A box inside a group's set is refused; drop it with
  `.without([...])`.
- Every directory a `scope()` names is drawn or excepted. A package that joins a layer
  joins the group built from that layer, and nothing else in the figure changes.
- Two or more code boxes need `edgesFromGraph()`, and every drawn directory needs a
  symbol index. An unindexed directory is a finding, never an empty edge list.
- A code edge is never drawn by hand. The hand edges each need an actor, and they claim
  `flow`, which nothing checks.

Declarations never raise. `draw()` and `diagram()` raise every finding at once, one per
line, each naming the call to change. `setOf`, `layerSet`, `without` and `plus` raise
immediately when handed something that is not a record.

## Tests

The module takes no host module. Its tests build `Dir`, `Layer` and `RefsResult` records
as map literals, the shape the host returns, and run with
`magus run test libs/figure`.
