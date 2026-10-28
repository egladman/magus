### Added

- **`prune-worktrees.buzz --apply` removes only the removable rows.** Each worktree goes
  only after the guard passes its `git worktree remove` line; `--go-cache` also empties
  the Go build cache, which is otherwise reported, never measured.
