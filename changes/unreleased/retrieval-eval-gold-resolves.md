### Fixed

- **The retrieval eval keeps gold that names a Go file or package dir.** It resolved
  gold against the default graph, which holds no `file:` or `dir:` node for a
  SCIP-indexed source, so most judgments lost every gold id and were dropped as
  unscorable; it now resolves against the graph with the symbol layer merged.
