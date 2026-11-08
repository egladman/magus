### Changed

- **Breaking: a run waits for the build budget by default, and `magus status --wait` is gone.**
  `budget_wait` defaults to 30 minutes, or the run's sooner deadline; 0 refuses at once.
  A parked run prints each holder's claim breakdown, its queue place, and the time
  waited and left; a time to seat says "at least". MGS3009 names the holders.
