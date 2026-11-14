### Changed

- **Breaking: a guard dry run copies its session into `magus-guard-dry-run-<uid>/` under the
  temporary directory.** The sweep for copies whose process has exited reads only that
  directory, not every entry of the temporary directory. Copies an older magus left at the
  temporary directory's root are no longer swept.
