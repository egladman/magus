### Fixed

- **`describe file` no longer calls a missing path a source.** A path that is not on disk
  and matches only wildcard globs comes back unclaimed, says it is absent and hints at the
  spelling; an exactly declared path stays a source.
- **The agents docs `format` target only formats.** `generate` now runs first in `ci`
  instead of inside `format`, so a docs worker's lease no longer has to cover the cursor
  schemas.
- **The checkout bootstrap installs dependencies** before it builds the graph, so a fresh
  checkout can run `affected ci`.
