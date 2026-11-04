### Added

- **Docs sections carry their line extents.** A `docsection` node now has `start_line`,
  `end_line`, `body_end_line` and a `digest` of its lines, and the `markdown` module's
  `sections` returns the same. The knowledge schema version is 25.
