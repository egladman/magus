### Fixed

- **A brief is refused exactly when its worker's run would be.** The spawn grader and
  `worker-check-only` share one verdict, so a brief teaching a slice of the check, an op
  the check composes or a dependency install is no longer refused at spawn.
