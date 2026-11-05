### Fixed

- **A worker's script can add and remove a detached worktree of its scratch repository.**
  vcs\cmd lets `git worktree add --detach` and `git worktree remove` through in a
  repository outside the job's checkout, for a literal path outside the checkout, the
  job's repository and `$XDG_CACHE_HOME/magus/tmp`. Branch-making adds stay refused.
