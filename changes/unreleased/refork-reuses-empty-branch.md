### Fixed

- **Forking a job id again after it ended no longer fails on its branch.** A checkout
  made for a branch that already exists, at the checkpoint, reuses it. A branch holding
  commits past the checkpoint is still refused.
