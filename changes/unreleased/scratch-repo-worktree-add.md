### Fixed

- **A worker's script can add a detached worktree of its scratch repository.**
  vcs\cmd lets `git worktree add --detach` through in a repository outside the
  job's checkout, for a literal path outside every checkout of the job's
  repository and `$XDG_CACHE_HOME/magus/tmp`. A `--detach` that is the value of
  `--reason` detaches nothing. Branch-making adds and every remove stay refused.
