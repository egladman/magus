### Changed

- **A worker can fast-forward to its binary's commit.** When the placed `./magus` was built
  ahead of the worker's checkout, `git merge --ff-only <commit>`, naming the commit by id, is
  the one way the worker may move its branch: the stale-binary deny and lease-vcs both let it
  through, and the stale-binary verdict says how.
