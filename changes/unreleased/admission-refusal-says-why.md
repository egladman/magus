### Fixed

- **A refused `job fork`, `session --brief` or `graph build` says why.** A full budget (MGS3009)
  or a short machine (MGS3053) exited 75 and printed nothing. A refusal raised outside a step,
  and an `ExitError` with a plain cause such as a paused job, now print their reason and next
  step once on stderr. The exit status stays 75.
