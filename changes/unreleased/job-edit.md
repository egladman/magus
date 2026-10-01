### Added

- **`magus job apply -f <file|->` upserts jobs' specs, the way `kubectl apply` does.**
  The record is the whole spec (criteria, write, deny and read paths, check, goals,
  model, depends_on, parent, timeout); the job keeps its state, holder and registration.
  `-f -` reads stdin, and a stream may hold one record, an array, or one per line, all
  checked before any is written. A new id creates the job. Only the orchestrator widens;
  a holder may apply only the release of its own paths. `--dry-run` prints the spec diff.
  A path dropped from a taken job is recorded as a revoked release with its digest, and
  the holder's next write there is refused naming the revocation. A refusal never serves
  a widening command.

### Changed

- **magus\job.put holds an update to fork's rules for what it adds.** Added goals and
  write paths now meet the same checks as a fork's (MGS3018, MGS3031, MGS3032).
