### Added

- **The docker spell has a `docker-load` op.** It runs `docker load` behind the spell's
  readiness probe, so a stopped daemon fails as MGS3004 rather than as a load error.
