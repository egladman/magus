### Added

- **A spell's cache declares the budget it is trimmed to.** `SandboxCache` gains `trimMb`,
  the MiB that reclaiming disk trims it back to, oldest files first and never one touched
  within a day. The go spell declares 20480 for `GOCACHE`.
