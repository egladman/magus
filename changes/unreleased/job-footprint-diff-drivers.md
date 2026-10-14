### Added

- **Diff drivers in the managed `.gitattributes` block**, with funcname patterns for
  TypeScript, Buzz and YAML. `magus doctor` reports a missing one.
- **A write path can claim one key of a magusfile or `magus.yaml`.**
  `magusfile.buzz#project.targets.test` and `magus.yaml#jobs.stale_after` let two jobs
  edit one configuration file. A write path that holds a project's magusfile or
  `magus.yaml` whole is refused (MGS3036).
