### Changed

- **A spawn for a read-only job with no check no longer hears the build budget is full.**
  Such a worker runs no target and holds no seat, so the advice to wait for one is skipped.
  A row with a check or write paths still gets it.
