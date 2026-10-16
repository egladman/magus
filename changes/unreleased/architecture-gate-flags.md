### Changed

- Hook wiring written by `magus describe harness` passes `--observes-skill-loads` on every
  judging entry, so skill gates apply to writes and commands too. `magus job fork` refuses a row with no write paths
  unless it is `--read-only`.
