### Fixed

- **A harness probe that runs out of time reports `unprobed`, not `uncovered`.** On a
  machine under memory pressure, a guard that answers in a second can miss the 10s probe
  deadline, and `magus agent harness verify` and doctor then called a wired config
  uncovered. A probe that misses its deadline or cannot start now reports `unprobed` and
  names the deadline.
