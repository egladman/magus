### Added

- **`reclaim-disk-space.buzz --apply` removes only the removable rows.** Each worktree goes
  only after the guard passes its `git worktree remove` line; `--go-cache` also trims the Go
  build cache to the go spell's `GOCACHE` budget by entry age, and never empties it.
