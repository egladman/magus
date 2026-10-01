### Changed

- **A running server answers symbol reads from memory.** `magus server` keeps the
  knowledge shards it decodes and the symbol-merged graph, so a warm `query --kind
  symbol`, `refs` or `explain` decodes nothing that did not change. A source edit, an
  index rebuild or a session load is read fresh. The cache is capped at 512 MiB of shards.
