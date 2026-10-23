### Fixed

- **A read-only job with no check or goal no longer passes on a result that names unresolved
  risks.** `magus job exit --stdin` records pass only when `unresolved_risks` is empty;
  otherwise the row stays exited for the root to read.
