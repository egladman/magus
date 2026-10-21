### Changed

- **The retrieval eval finishes on a full harvest.** `magus run retrieval-eval-test .`
  loads the knowledge graph once, with and without the symbol layer, and answers every
  judgment from it in parallel. The report adds rows per verb and per verb and split.
  Latency now measures an answer from a loaded graph, so an older baseline shows every
  latency as moved.
