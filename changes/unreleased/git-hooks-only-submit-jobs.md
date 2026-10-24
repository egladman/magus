### Changed

- **Breaking: git hooks only submit jobs.** `magus init --vcs git` installs no settle
  hook, so no commit waits on a regeneration; run `magus vcs resolve` after a merge.
  `magus vcs resolve --hook`, the `magus.settle` deferral and the `settle-hooks` and
  `deferred-settle` doctor checks are gone. Delete a leftover magus-regenerate section
  from your hooks by hand.
