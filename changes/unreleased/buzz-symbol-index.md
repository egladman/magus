### Added

- **Buzz gets a symbol index beside Go's.** A `SymbolIndexer` may declare its `op`, else
  it runs as `scip`. The buzz spell builds `scip-buzz`, which refs, rename and explain
  union with the Go index; a missing indexer is a gap with an install hint. A Buzz rename
  refuses keywords, a name in use, and a name used as an implicit label.
