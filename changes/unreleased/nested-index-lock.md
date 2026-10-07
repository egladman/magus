### Fixed

- **A dependency that names nothing, and a nested reindex refused its lock, now fail.**
  `ctx.glob` matching no exported target and `ctx.needs` naming no target raise instead
  of running nothing. `magus graph build` exits non-zero when an outer run's lock
  (MGS3007) refuses a reindex, without the misleading scip-go install hint.
