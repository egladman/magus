### Added

- **`magus\trail` reads what the guard did in a session.** `magus\trail.read` returns one
  session's guard record from every checkout's activity trail: each call with its verdict,
  rule, served nexts and command shape, plus the subagents it started.
  `magus\trail.shape` and `magus\trail.shapes` normalize a shell line so calls that differ
  only in their paths and literals read alike. `magus\trail.mark` and `magus\trail.marks`
  keep a person's verdicts on report rows per repository, so every worktree and later
  session sees them.
