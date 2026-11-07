### Added

- **The run band draws what each target waits on.** A tree under each running
  target lists the targets waiting on it, with their chains, how long each has run
  or waited, and the bottleneck holding the most. It replaces the `is waiting for`
  lines on a terminal; pipes, CI logs and `-o json` print them as before.
