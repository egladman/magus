### Changed

- **A fork whose workspace rules do not load keeps its row instead of refusing the
  check.** A checkpoint whose magusfiles need a newer binary than the forking one failed
  `job fork --check` with MGS3039. The fork now stores the row, says on stderr that the
  check was not graded and why, and leaves the guard to judge the check when the worker
  runs it with the checkout's own binary. A deny of the compiling check (a stale hook
  binary) still refuses.
