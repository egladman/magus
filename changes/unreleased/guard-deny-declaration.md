### Fixed

- **A deny path naming one declaration denies only edits to it.** A job forked with
  `--deny-paths 'run.go#A'` had every edit to `run.go` denied. The guard now places the
  edit the way it places one against a declaration claim, and denies it only when it
  changes `A`. A write it cannot place (no edit to apply, or a file no diff driver reads)
  is still denied whole. A file or glob deny path denies the whole file as before.
