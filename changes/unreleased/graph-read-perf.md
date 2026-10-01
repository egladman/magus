### Changed

- **Graph reads answer from the stored graph when nothing changed.** `query`, `explain`,
  `path` and `refs` no longer reassemble the whole knowledge graph per call. The store
  stamps each shard class's inputs (tree, history head, SCIP indexes, run records,
  coverage profile, session store, ignore rules outside the tree), answers from disk when
  they match, and reassembles only classes whose stamps moved.
