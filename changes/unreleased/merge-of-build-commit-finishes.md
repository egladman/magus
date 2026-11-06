### Fixed

- **A merge of the commit `./magus` was built at can be finished.** While `MERGE_HEAD` is
  that commit, the stale-binary rule lets `git add`, the merge's `git commit` and
  `magus vcs resolve` through. Before, a merge that brought a load-contract change could be
  committed only by hand, since the build from the dirty tree is refused too.
