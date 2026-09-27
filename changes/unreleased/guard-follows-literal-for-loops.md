### Fixed

- **The guard judges a `for` loop over literal words one iteration at a time.**
  `for b in x y; do git worktree remove /w/$b; done` passes when each worktree is
  safe to remove, and a dirty one is named in the denial. Lists needing expansion,
  other loops, conditionals and functions stay refused.
