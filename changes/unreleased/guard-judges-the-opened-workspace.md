### Fixed

- **A Go program's guard judges the workspace it opened, not the process's directory.**
  A run or Buzz `fs` write in a workspace opened at a directory with no magus file read
  the target policies of whatever workspace the process started in. A slow policy read
  timed the run out, and a `human_only` declaration there refused it.
