### Added

- **MGS3014: a newer gate supersedes an older one on the same tree.** The earlier `ci` run
  cancels and exits 75 (`EX_TEMPFAIL`). Sibling worktrees and non-`ci` runs are refused
  immediately instead, like any other contention.
