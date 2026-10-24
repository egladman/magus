### Fixed

- **Three small fixes for fresh checkouts.** `describe file` calls a missing path matched
  only by wildcard globs unclaimed and absent, not a source. The agents docs `ci` runs
  `generate` before `format`, so a docs lease need not cover the cursor schemas. The
  checkout bootstrap installs dependencies before it builds the graph.
