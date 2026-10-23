### Fixed

- **A denied background call no longer counts as a live task.** The guard's stop
  check starts a background task only when the call's result names its id, so a
  `run_in_background` call the guard refused or the host errored leaves nothing
  to stop.
