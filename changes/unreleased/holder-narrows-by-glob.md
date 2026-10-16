### Fixed

- **A job holder can narrow its write paths to the files its globs match.** `job apply`
  refused a holder's exact file list as widening because the files were not literally in
  its old glob list. Containment is now by glob match, so `internal/**/*.go` narrows to
  `internal/job/*_test.go` or to one file, and a child is handed a file its parent's glob
  matches. A glob rewritten into a wider one is still refused.
