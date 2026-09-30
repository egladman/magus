### Fixed

- **A worker in its own worktree can write inside its lease.** The guard grades a leased
  write in the checkout the lease was taken in, not the session's, and denies the same
  path in another checkout.
