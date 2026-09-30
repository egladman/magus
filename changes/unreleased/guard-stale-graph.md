### Fixed

- **The guard no longer refuses a search in favor of a stale graph.** While a
  merge, rebase, cherry-pick or revert is underway, or the guard index was built
  at another revision, symbol-search, grep-reader and search-translation advise
  that the graph is stale and name `magus graph build`. An index from another
  revision now reads as stale for every kind.
