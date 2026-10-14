### Changed

- **Breaking: a job's base is read from the checkout, never typed.** `magus job exec`
  drops `--base`, and `magus\job.register` takes only the job id; a checkout with no
  revision is refused. A run's output records the lease it ran under, and `job wait`
  refuses another lease's run, or one whose args after `--` differ from the check's.
