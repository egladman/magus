### Changed

- **The guard's graph-backed denies hold after a commit.** An index built at another
  revision used to turn them into advice, or into a deny serving `magus graph build` first,
  though a commit moves no file; the index's per-file stamps decide now. An index built
  during a merge, rebase, cherry-pick or revert still advises.
