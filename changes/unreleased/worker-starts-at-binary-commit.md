### Changed

- **`magus job fork --checkout` places `./magus` only at the commit it was built at.** A
  checkout at any other commit, a binary that records no commit, and one built from
  uncommitted changes are refused before the checkout is made, naming the commit to fork at. A worker there would have had every change denied until a new binary was placed.
- **A worker can fast-forward to its binary's commit.** When the placed `./magus` was built
  ahead of the worker's checkout, `git merge --ff-only <commit>`, naming the commit by id, is
  the one way the worker may move its branch: the stale-binary deny and lease-vcs both let it
  through, and the stale-binary verdict says how.
