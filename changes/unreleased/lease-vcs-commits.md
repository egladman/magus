### Changed

- **A worker may commit in its own checkout.** `lease-vcs` now lets a worker lease commit,
  with any backend, on its own branch in its leased checkout. It still refuses push,
  stash, revert, reset, clean, rebase, merge, cherry-pick, `checkout .`/`restore .`,
  worktree removal, and a commit in another tree, the primary checkout, the base branch or
  a detached HEAD.
