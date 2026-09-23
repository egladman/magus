### Changed

- **magus never waits on another magus invocation.** A workspace lock or machine budget
  held by another invocation refuses immediately (exit 75), naming the holder.
  `MAGUS_NO_WAIT` is removed. Invocations in one process (the daemon's) queue for each
  other, as do the nested runs of one root invocation. `--watch` retries on the next
  change.
