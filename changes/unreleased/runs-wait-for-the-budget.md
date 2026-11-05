### Changed

- **Breaking: a run can wait for the build budget, and `magus status --wait` is gone.**
  A claim that can fit waits in the broker's queue up to the new `budget_wait` (default
  `0`, refuse at once; `--budget-wait 30m` opts in); MGS3009 names the holders and how
  to wait. Reservations, the gate-only bound and the guard's pipe rules go. Broker protocol 5.
