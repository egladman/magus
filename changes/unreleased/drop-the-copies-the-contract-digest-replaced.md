### Removed

- **Breaking: the guard no longer refuses a state-changing line because `./magus` was built
  before a load-contract change `HEAD` carries.** That git-diff judgment is gone, and with it
  `Dependencies.BinaryCommit` and `Dependencies.BinaryDirty`. The contract digest decides
  whether `./magus` matches its tree; a cherry-pick or merge that changes the load contract
  is still refused up front.
