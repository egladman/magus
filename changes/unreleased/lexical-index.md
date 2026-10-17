### Added

- **The graph build writes one lexical index over every node and symbol.** It holds names
  split into words, paths, docs, signatures, doc comments and doc section bodies, scored
  by BM25F. Searchable text lives in sidecars under the knowledge store's `lexical/`
  directory, so graph nodes cost what they did, and the index sits at `@lexical.idx`.
  Queries do not read it yet.
