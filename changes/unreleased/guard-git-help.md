### Changed

- **The guard lets git's own help through its destructive-command rules.**
  `git stash --help`, `git worktree remove -h` and `git help stash` print usage
  and pass. The request has to be the whole line with no global option, prefix,
  pipe or flag before the help flag; anything else is judged as work.
