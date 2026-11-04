### Added

- **`fs\diskUsage`, `fs\trimOldest` and `fs\stateDir` join the `fs` module.** `diskUsage`
  reads a filesystem's total and free bytes (statfs, or `GetDiskFreeSpaceEx` on Windows),
  `trimOldest` removes a directory's least recently modified files a day of age at a time
  until it fits a budget and never one used within the last day, and `stateDir` names a
  per-user directory under `$XDG_STATE_HOME/magus`.
