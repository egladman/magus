### Changed

- **The retrieval eval finishes on a full harvest.** `magus run retrieval-eval .` loads
  the knowledge graph once, with and without the symbol layer, and answers every
  judgment from it in parallel. The report adds rows per verb and per verb and split.
  Latency now measures an answer from a loaded graph, so an older baseline shows every
  latency as moved.

### Fixed

- **The retrieval eval keeps gold that names a Go file or package dir.** It resolved
  gold against the default graph, which holds no `file:` or `dir:` node for a
  SCIP-indexed source, so most judgments lost every gold id and scored as
  unscorable; it now resolves against the graph with the symbol layer merged.
