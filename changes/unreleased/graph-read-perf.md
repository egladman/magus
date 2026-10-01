### Changed

- **Graph reads answer from the stored graph when nothing changed.** `query`, `explain`,
  `path` and `refs` used to reassemble the whole knowledge graph on every call. The store
  now records a cheap stamp of each shard class's inputs (the tree, the history's head,
  the SCIP indexes, run records, the coverage profile, the session store, and git's ignore
  rules outside the tree), answers from disk when the stamps match, and reassembles only
  the classes whose stamps moved. A domain read no longer parses any SCIP index, a parsed
  index is reused until its file changes, and a symbol read no longer rebuilds the agent
  session overlay every time the session store grows.
- **A symbol query ranks from a names index.** `query --kind symbol` used to decode every
  symbol shard. It now ranks matches from a compact index of node names and decodes only
  the shards its answer's neighborhood touches, with the same output.
- **`refs` reads one directory's symbols, not the whole index.** Symbol shards are split
  by the directory a symbol is defined in, a bare name routes to the shards of the symbols
  it names, and `--occurrences` reads each symbol's sites from a file written once per
  index instead of decoding the SCIP index on every call.

### Fixed

- **`refs` refuses a name several definitions share.** It used to answer for whichever one
  ranked first. It now exits 2 and lists `magus refs '<id>'` for each candidate.
