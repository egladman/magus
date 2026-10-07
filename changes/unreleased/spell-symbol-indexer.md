### Fixed

- **A local spell registered by value keeps its symbol indexer.** A spell passed to
  `magus.project` by value lost its `mgs_getSymbolIndexer` declaration, so the auto-indexer
  and index status skipped it, and a spell that only indexes warned that it exposes no
  targets.
