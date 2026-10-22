### Changed

- **`show-failures` reads a failed plain CI job's log.** When a run has no `magus-logs-`
  artifact but a job failed (`magus doctor`, for one), it prints the tail of each failed
  job's `gh run view --job <id> --log-failed` and says on stderr that it fell back.
