### Added

- **`magus job fork --checkout <dir>` makes the job its own checkout and removes it when
  the job ends.** It is a git worktree, an hg share or a jj workspace at the job's
  checkpoint, holding a copy of the forking checkout's own `./magus` when it runs one. The
  job is taken there and nowhere else. Once the job ends, `job wait`, `job exit` and
  `job prune` remove a clean checkout whose commits a branch, bookmark, remote or its base
  holds, and record why they kept any other. The guard lets the job's holder and the
  session that forked it run magus in that checkout and remove it, refuses anyone else's
  removal, and points a worker's `git worktree add`, `hg share` or `jj workspace add` at
  the flag. Sapling has no second checkout and refuses.
