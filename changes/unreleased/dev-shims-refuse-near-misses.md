### Fixed

- **The release refuses a near miss of the development-only marker.** A condition that
  differs from `the next release` only in case, spacing or a trailing period is named by
  `file:line`, and a tracked directory entry is listed instead of aborting the scan.
- **A job result that reports its check unrun names the missing check.** A job with goals
  and no check now fails on `validation.unrun` instead of dropping it.
