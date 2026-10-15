### Added

- **Job rows carry generations (schema 13).** `generation` counts changes to a job's declared spec, as a Kubernetes object's does, and each result, run record and grade records the `observed_generation` it was taken against, so evidence from an earlier spec reads as stale. A row needing these is read-only to an older magus.
