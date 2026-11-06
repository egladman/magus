### Fixed

- **A claim whose wait turns to its own run's steps no longer expires at `budget_wait`.** The
  bound clears when the wait turns to its own run, and starts again if another invocation
  keeps the claim out later.
- **A parked claim's status line no longer goes stale.** The broker sends a fresh parked frame
  whenever remaining memory, remaining slots, holders or the verdict change, and never lets
  an older frame land after a newer one.
