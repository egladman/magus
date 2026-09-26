### Fixed

- **The merge queue starts a validation run only when no unfinished one covers the
  change.** On merge intent, and when CI finishes on a queued head, it skips if a run on
  main is pending or planned later, so triggers no longer cancel each other's pending
  runs. Each decision is a named notice.
