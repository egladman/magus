### Added

- **`concurrency_profile` sets build width relative to the machine.** `conservative` (half
  the cores), `balanced` (`min(cores, 8)`, the default) or `aggressive` (every core), also as
  `--concurrency-profile` and `MAGUS_CONCURRENCY_PROFILE`. An explicit `concurrency`
  overrides it.
