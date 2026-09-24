### Added

- **A spawn rule sees its continue target's facts and the job store.** `target` carries
  the spawn's `description`, `model` and last observed `contextTokens`;
  `magus\job\list()` reads the guard's rows. A spawn titled `<parent>/<role> <job>`
  grades the child under that job's lease and records its base.
