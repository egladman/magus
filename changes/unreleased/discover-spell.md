### Added

- **The `discover` spell finds code from the knowledge graph, a bounded layer at a time.**
  It ranks the symbols, files and doc sections a term names, then opens each to its line,
  its whole declaration and its callers, tests and docs. A stale or missing index answers
  unknown, never empty. `hack/dev/show-code-context.buzz` is its command line.
