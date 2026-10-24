### Fixed

- **A worker's wait on another run's process is refused as a budget wait.** `caffeinate
  -w`, `wait`, `lsof -p` and `kill -0` on a seated run's pid get the `sleep-poll` remedy,
  and the root retrying a refused run is told to fork a job.
