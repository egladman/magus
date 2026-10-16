### Fixed

- **The refused-retry rule no longer refuses a command whose newest run got its seat.** A
  command refused and then run again successfully within the minute passes. Only a run
  that a `status --wait` pipes into now counts as waited: a wait anywhere else on the
  line, or a `--watch`, no longer clears the line.
