### Fixed

- **`git worktree remove` passes a clean, landed worktree whose job exited without a
  result.** Nothing was filed to wait for. A dirty or unlanded tree still denies and
  names `job exit`.
