### Added

- **`magus job apply -f <file|->` upserts jobs' specs, the way `kubectl apply` does.** The
  record is the whole spec; the job keeps its state, holder and registration. `-f -` reads
  stdin: one record, an array, or one per line, all checked before any is written. A new
  id creates the job. `--dry-run` prints the spec diff.
