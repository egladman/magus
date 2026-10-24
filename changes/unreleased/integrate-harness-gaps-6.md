### Fixed

- **Job stores write as the running build and skip generated outputs.** `magus job fork`,
  `magus job wait` and the daemon's job tool keep the build's stamp instead of being
  refused with MGS3051, and a run is no longer stale because a declared output changed
  after it.
