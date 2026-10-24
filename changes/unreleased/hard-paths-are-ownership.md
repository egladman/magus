### Fixed

- **A soft write path no longer blocks another job.** A test file a fork adds beside a
  job's writes stays free until that job edits it. `magus describe job`, the guard and
  release requests stopped treating it as owned. Once the holder edits it, describe and the
  guard read it as owned at once.
