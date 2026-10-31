### Added

- **`reclaim-disk-space.buzz --apply` removes only the removable rows.** Each worktree goes
  only after the guard passes its `git worktree remove` line; `--caches` also trims each
  cache to the budget its spell declares, keeping files inside `--keep-within`.
