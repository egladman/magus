### Changed

- **A running server answers symbol reads from memory.** `magus server` keeps the knowledge
  shards it decodes, and the symbol-merged graph with its adjacency, bound to the files and
  fingerprints they came from, so a warm `query --kind symbol`, `refs` or `explain` decodes
  nothing that did not change. A source edit, an index rebuild or a session load is read
  fresh. The cache is capped at 512 MiB of shards. A symbol-seeded `magus query` ranks from
  the names index and decodes only the shards its answer touches, with byte-identical output.
