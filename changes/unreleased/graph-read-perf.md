### Changed

- **Graph reads answer from the stored graph when nothing changed.** `query`, `explain`,
  `path` and `refs` used to reassemble the whole knowledge graph on every call. The store
  now records a cheap stamp of each shard class's inputs (the tree, the history's head,
  the SCIP indexes, run records, the coverage profile, the session store), answers from
  disk when the stamps match, and reassembles only the classes whose stamps moved. A
  domain read no longer parses any SCIP index.
