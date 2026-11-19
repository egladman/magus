### Changed

- **Breaking: figure is renamed diagram everywhere.** The Buzz module is
  `import "magus/diagram"` and the library is `libs/diagram`. `figure\Figure` is
  `diagram\Diagram`, the laid-out `Diagram` is `diagram\Layout`, `.diagram()` is
  `.layout()`, `drawDiagram` is `drawLayout` and `atlas(...).diagram(f)` is `.layout(f)`.
  `kustomize\figureOf` is `kustomize\diagram`. No alias keeps the old names.
- **Breaking: the graph kind is `diagram`.** Query `kind=diagram`; a node id is
  `diagram:<id>`. `magus version` reports `embedded.diagram_sha256`.
- **Breaking: a console link carries its graph in `#diagram-link=`.** `#diagram=` already
  names the diagram a view shows, so a `#figure=` link no longer opens. The Graph's mode
  is `diagrams`, and the playground's runtime exposes `buzz.drawDiagram`.
- **Breaking: an inlined diagram resolves its paints through `--magus-diagram-paint-*`.**
  The page still sets `--magus-diagram-*`; only the SVG's own `--magus-figure-*` names
  changed. An SVG's `<metadata>` names its id under `"diagram"`.
