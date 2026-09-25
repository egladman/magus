### Added

- **`search-translation` denies a search a graph query provably answers.** A pattern
  matching only MGS codes, every Markdown heading searched, or only magusfile target
  declarations is checked against the graph's ids and names the exact query. The guard
  reads those ids from `guard.idx`, written by `magus graph build`, within 150 ms.
