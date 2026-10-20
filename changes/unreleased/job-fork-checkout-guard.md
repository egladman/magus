### Added

- **The guard lets the job's holder and the session that forked it run magus in that checkout and remove it.** It refuses anyone else's removal, and points a worker's `git worktree add`, `hg share` or `jj workspace add` at the flag.
