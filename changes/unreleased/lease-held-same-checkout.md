### Fixed

- **A lease in one checkout no longer blocks a run in another over an ignored output.**
  A worker's `test .` was refused because `.magus/coverage.cmd.out` was held by a lease
  taken in a different worktree. An output the VCS ignores is a file of each checkout, so
  the guard now makes the run wait only for a holder in the same checkout, or for any
  holder of an output the change commits.
