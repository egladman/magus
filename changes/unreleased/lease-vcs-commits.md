### Changed

- **A worker may commit in its own checkout.** `lease-vcs` now lets a worker lease commit,
  with any backend, cherry-pick or `git apply`, on its own branch in its leased checkout:
  a cherry-pick and `git diff | git apply -3 && git commit -C` get one answer. It
  still refuses push, stash, revert, reset, clean, rebase, merge, `checkout .`/`restore .`,
  worktree removal, and a commit in another tree, the primary checkout, the base branch or
  a detached HEAD. A worker starts on the job's branch: `git switch <job>`, or
  `git switch -c <job>` when the orchestrator made none, and the refusal on a detached HEAD
  names that line.
