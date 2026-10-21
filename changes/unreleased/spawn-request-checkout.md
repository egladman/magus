### Changed

- **A spawn request carries the checkout it runs in.** The Buzz spawn rule reads it
  with each job row's bound checkout, so an integrate spawn for a row bound elsewhere is
  refused. The worktree bootstrap skips the harness skill install under a lease.
