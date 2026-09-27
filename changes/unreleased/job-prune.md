### Added

- **`magus job prune` ends every job nobody is working.** Exited and never collected,
  overdue, orphaned, or never taken past `jobs.stale_after`: each ends as `no_return`
  and prints with its reason, then the count. A job its holder touched within
  `jobs.stale_after` is never ended; `--all` also ends one idle that long.
