### Fixed

- **A job holder can narrow its write paths to the files its globs match.** `job apply`
  refused a holder's exact file list as widening when its glob list did not name them.
  Containment is now by glob match, so `internal/**/*.go` narrows to one file, and a child
  gets a file its parent's glob matches. Widening a glob is still refused.
