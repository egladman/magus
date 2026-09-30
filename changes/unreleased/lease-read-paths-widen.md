### Fixed

- **A lease's read paths widen its write paths instead of replacing them.** A job
  declaring `--read-paths` lost read access to the projects it was leased to edit, so
  the guard's `focus-read` rule denied a worker its own files. The guard now reads the
  same boundary the job store checks: the write paths plus the read paths.
