### Fixed

- **A deny path naming one declaration denies only edits to it.** A job forked with
  `--deny-paths 'run.go#A'` had every edit to `run.go` denied. The guard now places the
  edit the way it places one against a declaration claim, and denies it only when it
  changes `A`. `magus job wait` grades the footprint the same way, and a write elsewhere
  in the file is still attributed to the job. A write the guard cannot place is denied
  whole. A file or glob deny path denies the whole file as before.
- **`magus job fork` refuses a deny path no footprint can grade (MGS3031).** A
  declaration deny on a file with no diff driver, on a pattern, or with nothing after
  the `#` is refused, as the same write path already was.
