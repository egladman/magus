### Fixed

- **A worker in a fresh linked checkout can take its job there.** `magus job exec` binds a
  job nobody took, whose fork adopted another checkout, to the checkout it runs in, when that
  checkout is at the job's checkpoint. The guard no longer refuses that worker's commands
  before it takes the job.
