### Fixed

- **A worker in its own worktree can write inside its lease.** A leased write is graded
  against the checkout the lease was taken in, and a write into another magus checkout
  is denied. A subagent's first call registers its checkout only when the host reports
  the call's directory; otherwise `magus job exec` does.
