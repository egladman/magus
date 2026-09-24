### Added

- **The guard advises on reads outside the session's focus.** Focus is the working
  project, its dependencies and nested projects. Under a bound lease it denies, and the job
  row's `read_paths` grants reads without writes. `magus describe file` reports `focus`.
