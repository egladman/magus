### Added

- **The `discover` spell finds code from the knowledge graph, a bounded layer at a time.**
  `spells/discover` ranks the symbols, files and doc sections a term names as path:line,
  then opens the first few to their line in context, their whole declaration from the
  symbol index's recorded end line, and their callers, callees, tests, references, owning
  project and docs. Every list is capped and counts what it cut; a stale or missing index
  answers unknown, never empty. `hack/dev/show-code-context.buzz` is its command line,
  with magus's own `-o` formats.
