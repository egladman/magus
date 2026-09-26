### Fixed

- **A worker in its own worktree is graded under its job.** A spawn titled
  `<parent>/<role> <job>` now binds the child's agent id beside the job store, so its
  hooks resolve the lease from any checkout, and its first call records that checkout's
  base. A title's `<job>` also matches `<parent>/<job>`.
