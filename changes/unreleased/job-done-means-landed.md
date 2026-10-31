### Added

- **A job is not done until it is merged.** `magus\job.unmerged` reports what the checkout's
  branch does not hold: filed or ended work, uncommitted paths, and deferred items. `magus ls
  jobs` and the session brief list them, and this repository's stop rule refuses on them.
  A result gains `follow_ups`; a `job apply` record with `deferred` or `left_unmerged` answers.
