### Changed

- **`magus status --wait | magus run ...` keeps the seat it waited for.** The wait now has
  the broker reserve the seat it sized for the magus run reading its output, and that
  run's first claim takes it, so another run can no longer take it in between and refuse
  the piped run with MGS3009. The reservation ends when the reader exits, or after 30
  seconds without a claim. A bare `magus run` still refuses at once when the budget is
  full.
- **A leased worker may wait for its own check's seat.** The guard allows a worker to run
  `magus status --wait | magus run <check>`, and names that pipe when it refuses a rerun
  within a minute of an MGS3009 refusal. A wait that feeds no run, a watch, or a sleep
  before a run is still refused.

### Fixed

- **The refused-retry rule no longer refuses a command whose newest run got its seat.** A
  command refused and then run again successfully within the minute passes. Only a run
  that a `status --wait` pipes into now counts as waited: a wait anywhere else on the
  line, or a `--watch`, no longer clears the line.
