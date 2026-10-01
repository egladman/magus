### Added

- **`hack/dev/prune-worktrees.buzz` plans what the repository's worktrees can give back.** It
  ranks every worktree and agent branch by what removing it reclaims, keeps any worktree with
  uncommitted files, commits no remote or base has, a live job, a lock, a process working in
  it, recent activity or the caller in it, and names each reason. The shared Go build cache
  is reported, never measured. `--apply` removes only the removable rows, each worktree only
  after the guard passes its `git worktree remove` line; `--go-cache` also empties the Go
  build cache through `go::go-clean`.
