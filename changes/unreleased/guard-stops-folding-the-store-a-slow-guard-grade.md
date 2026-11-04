### Fixed

- **A slow guard grade no longer reads as a denied check.** `job fork --dry-run` gives
  the workspace open its full two minutes, and a judgment that runs out of time defers
  the check's grade instead of reporting MGS3040. A real deny still refuses.
