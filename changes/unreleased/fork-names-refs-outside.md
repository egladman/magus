### Added

- **`magus job fork` and `magus describe job` name the files outside a job's write paths
  that use what its write paths define.** Tests come first, each with the write paths it
  references, so a file one hop out is added before the job starts. It never refuses a
  fork. `-o json` carries it as `referenced_outside`.
