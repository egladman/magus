### Changed

- **A job whose work landed on the base ends by itself.** An exited job's changed paths
  are compared, offline, at its checkout's HEAD and at the base branch; when every one
  matches, the job ends as `no_return` with an end reason naming the base commit. A
  squash that holds several jobs' branches ends each of them. Each job is probed at most
  once every five minutes.
- **An exited job no longer blocks another fork's write paths.** Its holder returned, so
  the shared-checkout refusal and MGS3032 leave its paths alone.
- **Re-forking a live job to move its paths keeps its state.** A fork that only changes
  write, read or deny paths no longer resets the job to `declared`.

### Fixed

- **A job with a checkout root but no registration counts as never taken.** It ends
  past `jobs.stale_after` like any other job nobody took.
