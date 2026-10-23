### Fixed

- **A worker's wait on another run's process is refused as the budget wait it is.**
  `caffeinate -w`, `wait`, `lsof -p` and `kill -0` on the pid of a run holding a seat get
  the same `sleep-poll` denial and remedy as `status --wait` outside the call that runs.
- **The root is told to fork a job when a refused run is retried.** The piped
  `status --wait | magus run ...` line it was served is one `orchestrator-groundwork`
  refuses the root.
