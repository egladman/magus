### Added

- **A write path can claim one key of a magusfile or `magus.yaml`.** `magusfile.buzz#project.targets.test` and `magus.yaml#jobs.stale_after` let two jobs edit one configuration file. A write path that holds a project's magusfile or `magus.yaml` whole is refused (MGS3036).
