### Removed

- **Breaking: the guard no longer judges whether a state-changing line may run from the
  commit `./magus` was built at.** That git-diff judgment is gone, and with it
  `Dependencies.BinaryCommit` and `Dependencies.BinaryDirty`. The contract digest decides
  instead: while the binary's digest differs from the one its tree carries, the guard refuses
  every state-changing line before any load and names the relink as the one remedy. Reads, VCS
  recovery and the relink still run, and a tree that carries no contract never differs. A
  cherry-pick or merge that changes the load contract is still refused up front.
