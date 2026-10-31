### Added

- **The `search` spell finds code from the knowledge graph, a bounded layer at a time.**
  It ranks the symbols, files and doc sections a term names, then opens each to its line,
  its whole declaration and its callers, tests and docs. A stale or missing index answers
  unknown, never empty. `magus refs --occurrences --source` prints each site's code.
