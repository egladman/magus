### Added

- **`import "magus/feedback"` reads what the agent guard did in a session.** `feedback\trail`
  returns one session's guard record from every checkout's activity trail: each call with
  its verdict, rule, served nexts and command shape, plus the subagents it started.
  `feedback\shape` and `feedback\shapes` normalize a shell line so calls that differ only in
  their paths and literals read alike. `feedback\mark` and `feedback\marks` keep a person's
  verdicts on report rows per repository, so every worktree and later session sees them.
