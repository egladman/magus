### Added

- **`hack/dev/prune-worktrees.buzz` plans what the repository's worktrees can give back.**
  It ranks worktrees and agent branches by what removing them reclaims, and keeps any with
  uncommitted files, commits no remote or base has, a live job, a lock, a process working
  in it, recent activity or the caller in it, naming each reason.
