### Changed

- **A finished branch is integrated once its content reached main, however it merged.**
  The fork facts behind the finished-branch limit and `magus ls jobs`'s "not merged"
  list ask no ancestry. A branch is integrated when no path it changed reads otherwise,
  when merging it changes nothing, or when a commit since the split reads as it on every
  changed path. The remote default branch (git's `origin/main`) counts as well as the
  checkout's own revision, so a squash merged on the forge reads as integrated before
  the checkout pulls it. A remote whose default branch does not read is named in the
  answer. The answers move to `reached.jsonl`; `missing-commits.jsonl` is no longer read.
- **Breaking: `magus job prune <job>...` scopes the prune to the jobs named and their
  descendants.** It ended every stale row before. A named job it would not end refuses
  the prune, and a job ending only with its ancestor is named together with that
  ancestor (`magus job prune done done/c`).
- **Breaking: a rejected `magus job wait` whose rework cannot re-claim a path it changed
  ends the row as `fail`, its rejection recorded.** The row stayed exited and ungraded,
  and the guard refused removing its checkout until a prune.
