### Fixed

- **The job store judges a job by what it declares.** A job queued on a live dependency
  is no longer ended as stale; its clock starts when the dependency ends. A re-fork is
  ordered by its new depends_on, a put adding a directory write path is refused
  (MGS3018), and an abbreviated checkpoint no longer reads as a diverged base.
