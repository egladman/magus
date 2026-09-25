### Changed

- **Breaking: magus ends dead jobs itself, and job rows are `schema_version` 10.** A
  store read ends a live job as `no_return` when an ancestor ended, its checkout is gone,
  or nobody took it within `jobs.stale_after` (default 2h, `0` never). Older magus
  binaries refuse the store once this one writes it: restart the daemon and rebuild
  `./magus` after upgrading.
