### Fixed

- **A reader inside a target now freshens the symbol index without a refused nested run.**
  The reindex runs in the outer run, under the lock it holds. `ctx.glob` matching
  nothing and an empty `ctx.needs` now raise, and `magus graph build` exits non-zero
  when an outer run's lock (MGS3007) refuses a reindex.
