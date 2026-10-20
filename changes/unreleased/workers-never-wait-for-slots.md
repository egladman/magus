### Changed

- **A worker never waits for the build budget; the orchestrator does.** `sleep-poll`
  refuses a leased worker's wait for a seat: `magus status --wait` or `--watch`, a status
  read beside a sleep, or a run after one. The worker commits, reports no seat, and stops.
  A spawn or fork into a full budget says how full and who holds it longest.
