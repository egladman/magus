### Added

- **The graph build writes one lexical index over every domain node and code symbol.** It
  holds each node's name split into words, its path, doc, signature, a symbol's doc comment
  and a doc section's body text, scored by BM25F. Section bodies stay out of the graph's
  nodes, so explain, export and loading cost what they did; each domain and symbol shard
  keeps its searchable text in a sidecar under the knowledge store's `lexical/` directory,
  and the index lives at `@lexical.idx` beside the manifest. Queries do not read it yet.
