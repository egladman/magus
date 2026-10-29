### Fixed

- **A gate record names its scope.** A passing `magus run ci libs/cmdline` no longer reads
  as the branch gate. A record is `affected` when it covered every project the branch
  affects, and `project` otherwise; only an `affected` pass covers a push, and a failure
  of either keeps the branch red.
