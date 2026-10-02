### Changed

- **A graph read no longer evaluates the workspace when the stored graph is current.**
  `magus query`, `explain`, `path`, `graph stats` and `graph export` open the workspace
  lazily. The knowledge store records a cheap "fast stamp" per shard class beside the
  full one: the tree, the binary, the configuration, the cached provider answers, and
  every environment variable and file the last evaluation's magusfile top levels read.
  A read whose fast stamps match answers from the stored shards without parsing a
  magusfile; one whose do not falls back to the full stamps and the rebuild as before,
  and `--refresh` ignores both. The declared symbol indexes, each with the freshness
  verdict that evaluation reached and the identity of the index file it judged, are
  recorded too, so such a read reports the same coverage and the same stale indexes it
  did when it evaluated the workspace, and judges afresh the moment an index file or the
  sources differ. Measured on this repository: a warm `magus query` drops from about
  590ms to about 290ms, of which 425ms was parsing magusfiles the read never used.
