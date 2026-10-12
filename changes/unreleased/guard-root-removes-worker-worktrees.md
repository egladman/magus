### Changed

- **The root session removes its finished workers' worktrees.** `git worktree remove
  -f -f` passes over a lock its spawned worker's host holds once that job ends pass or
  fail. Unpublished commits pass when their changes read the same in your HEAD, for
  every job state. `git worktree unlock` is judged like a removal.
