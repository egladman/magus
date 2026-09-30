### Changed

- **A lease binding whose checkout is gone becomes a tombstone the guard refuses.** The
  read that ends a job whose worktree was removed turns each binding naming it into a
  tombstone. The guard refuses that caller with `lease-undeclared` until `magus job exec`
  binds it again, instead of grading it as the orchestrator. Tombstones expire after
  `jobs.stale_after`.
