### Fixed

- **A deny path naming one declaration denies only edits to it.** A job forked with
  `--deny-paths 'run.go#A'` had all of `run.go` denied. The guard now denies an edit only
  when it changes `A`, and `magus job wait` grades the footprint the same way. A write it
  cannot place, and a file or glob deny path, deny the whole file.
