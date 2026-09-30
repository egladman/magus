### Changed

- **Breaking: a magus/figure `Figure` is plain data, drawn by `figure\draw(f, theme:)`.**
  `.svg()`, `.title()`, `.eyebrow()`, `.desc()`, `.down()` and `.generated()` are gone:
  `figure\of(id, title:, eyebrow:, desc:, direction:, generated:)` sets them. The Diagrams
  server and the console runtime build a `Figure` and call `draw` without writing Buzz, and
  the playground resolves `import "magus/figure"`.
