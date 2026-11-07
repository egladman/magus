### Fixed

- **A job's Go check can name the packages it writes.** `go-test . -- ./internal/job`
  runs only that package, and `magus job fork` now grades it: a target whose body is
  `ctx.needs` plus spell calls forwards its `--` args, so MGS3040 reads its selector. A
  check naming every written package may narrow with `-run`; one leaving a package out
  stays refused.
