### Changed

- **`show-failures` reads a failed plain CI job's log.** A failed job that no `magus-logs-`
  artifact accounts for (`magus doctor`, for one, even beside shards that uploaded theirs)
  gets the tail of its `gh run view --job <id> --log-failed` printed, and a line on stderr
  says it fell back.
