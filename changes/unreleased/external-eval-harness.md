### Added

- **An external retrieval benchmark scores every system in one harness.** It reads the
  CoIR subsets, runs BM25 and potion-code-16M-v2, and takes CodeRankEmbed runs from
  `embed bench`. Each run is a trec_eval file scored by `rankeval.ReadRun`, ties broken
  by an id hash, with 95% intervals. cosqa runs deduplicated too, and csn-go queries
  lose the function name they start with.
