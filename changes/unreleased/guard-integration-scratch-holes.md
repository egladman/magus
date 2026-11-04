### Fixed

- **The guard refuses `git -C <other repo> worktree add` and ref moves an integration row
  could slip past.** A row with no standing worktree is held to the same refusals.
