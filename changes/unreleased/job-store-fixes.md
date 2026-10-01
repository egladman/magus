### Fixed

- **`magus job wait` accepts a check's own run.** A check resolves against the workspace's
  `default_charms` as `magus run` does, so with default `rw`, `magus run test .` records
  `test:rw` and satisfies a `test .` check. A run of another target, project or charm set
  is still rejected. A check declaring `no_default_charms: true` is satisfied only by a
  `--no-default-charms` run.
