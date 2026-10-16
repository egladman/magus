### Changed

- **The retrieval eval finishes on a full harvest.** `magus run retrieval-eval .` loads
  the knowledge graph once, with and without the symbol layer, and answers every
  judgment from it in parallel, rendering each answer once to count the bytes `query`,
  `refs` and `explain` print. The report adds a row per verb and per verb and split, so
  dev and holdout read apart for each verb. Latency now measures an answer from a
  loaded graph, the read the daemon serves, not a cold CLI read that loads the graph
  first, so a baseline written before this change shows every latency as moved.

### Fixed

- **The retrieval eval keeps gold that names a Go file or package dir.** It resolved
  gold against the default graph, which holds no `file:` or `dir:` node for a
  SCIP-indexed source, so most judgments lost every gold id and scored as
  unscorable; it now resolves against the graph with the symbol layer merged.
