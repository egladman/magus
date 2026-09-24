### Added

- **`magus diff` brings the symbol index current before it reads it.** Each touched
  project's `scip` target replays or rebuilds; when it cannot (a missing indexer, cache
  writes off), the review carries `MGS7003` in place of conformance findings, and the PR
  section fails its step instead of reading as clean.
