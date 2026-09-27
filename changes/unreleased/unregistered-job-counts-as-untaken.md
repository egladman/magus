### Fixed

- **A job with a checkout root but no registration counts as never taken.** It ends past
  `jobs.stale_after` like any other job nobody took.
