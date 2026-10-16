### Changed

- **A worker never waits for the build budget; the orchestrator does.** The agent guard's
  `sleep-poll` rule refuses a leased worker's wait for a seat, in a shell call or a watcher:
  `magus status --wait` or `--watch`, a status read beside a sleep or inside a watcher, and
  a magus run after a sleep. The worker commits, reports that its check found no seat, and
  stops. A spawn and `magus job fork` into a full budget say how full it is and which run
  has held it longest, so the orchestrator waits before starting the worker.
