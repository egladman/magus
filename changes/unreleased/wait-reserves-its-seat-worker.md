### Changed

- **A leased worker may wait for its own check's seat.** The guard allows a worker to run
  `magus status --wait | magus run <check>`, and names that pipe when it refuses a rerun
  within a minute of an MGS3009 refusal. A wait that feeds no run, a watch, or a sleep
  before a run is still refused.
