### Changed

- **The job store ends a subagent's lease binding once its job's checkout is gone.**
  The read that ends a job whose worktree was removed also turns every session or
  subagent binding naming that job into a tombstone. The guard refuses a tombstoned
  caller's work with `lease-undeclared` until `magus job exec` binds it again, rather
  than grading it as an unbound orchestrator. Tombstones are removed after
  `jobs.stale_after`, so bindings no longer pile up one file per spawn.
- **The job store grades `magus job wait`'s verdict itself.** A leased holder may pass a
  row below its own lease and change nothing else on it; any other verdict is refused by
  the store, not only by the command.
