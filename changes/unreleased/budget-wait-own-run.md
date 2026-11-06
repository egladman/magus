### Fixed

- **A claim whose wait turns to its own run's steps no longer expires at `budget_wait`.** The
  bound clears when the wait turns to its own run, and starts again if another invocation
  keeps the claim out later.
