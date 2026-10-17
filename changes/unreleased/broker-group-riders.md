### Fixed

- **A step's process group the broker stops is filed in the run's attention queue.**
  The broker recorded the stop only in its own log and in `magus status`, because the
  run never told it which workspace the group ran in.
