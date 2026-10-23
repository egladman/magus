### Added

- **A worker can claim a path by proof through `magus job apply`.** Send `id` and `claim` with each path proved by `refs:<symbol>`, `test:<file>`, `output:<project>:<target>` or `released`; the store re-proves it and records it on `claims`. `max_claims` caps a job (default 3); `jobs.max_claims` in `magus.yaml` (default 10) is the ceiling.
