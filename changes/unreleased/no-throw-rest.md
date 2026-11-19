### Changed

- **`magus/diagram` returns errors as values.** `Diagram.layout()` and `draw()` return a
  `Result`, and the release wizard's version and dry-run helpers do the same; their
  no-throw baseline rows fall. `without`, `drawLayout` and `Atlas.layout` still raise
  while the docs diagrams call them.
