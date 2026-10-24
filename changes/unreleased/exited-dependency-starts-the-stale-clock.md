### Fixed

- **A declared job queued behind an exited one goes stale.** A dependency counts as
  holding its dependent only while it is declared or running. Once it exits, the
  dependent's `jobs.stale_after` clock starts at the exit, so a row whose dependencies
  exited and were never collected is ended instead of waiting forever.
