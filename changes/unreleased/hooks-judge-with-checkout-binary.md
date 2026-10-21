### Changed

- **Claude Code hooks judge with the checkout's own `./magus`.** The hook command runs
  `$CLAUDE_PROJECT_DIR/magus` when it is executable and falls back to the `magus` on PATH.
  A stale-binary guard now lets the worktree placement and the bootstrap through for every
  caller, so its fix is never denied.
