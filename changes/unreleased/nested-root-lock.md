### Fixed

- **A nested magus aimed at another workspace runs there.** `magus --root <other> affected
  generate:rw` started from inside a running `magus run` was handed to the outer run's
  process, which ran it against the outer workspace, so the outer run's own lock on
  project `.` refused it with MGS3007. The outer run now declines a run from another
  workspace and the nested magus runs it itself, under that workspace's locks. A nested
  run back into the same workspace's project is still refused.
