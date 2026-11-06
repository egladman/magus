### Fixed

- **A parked claim's status line no longer goes stale.** The broker sends a fresh parked frame
  whenever remaining memory, remaining slots, holders or the verdict change, and never lets
  an older frame land after a newer one.
