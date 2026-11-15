### Removed

- **Breaking: the guard no longer judges a state-changing line from the commit `./magus`
  was built at.** `Dependencies.BinaryCommit` and `Dependencies.BinaryDirty` go with it.
  While the binary's contract digest differs from its tree's, the guard refuses every
  state-changing line and names the relink. Reads, VCS recovery and the relink still run.
