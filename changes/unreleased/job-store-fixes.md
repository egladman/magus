### Fixed

- **`magus job wait` accepts a check's own run.** A check is resolved against the
  workspace's `default_charms` the way `magus run` resolves it, so in a workspace whose
  default is `rw`, running `magus run test .` records `test:rw` and satisfies a `test .`
  check. A run of another target, project or charm set is still rejected, and the
  suggested command no longer appends `--no-default-charms`.
- **A goal's `expect` is optional in the job schema.** The decoder already filled it in
  (`passed` for a check, `changed` for paths and symbols); `magus job fork --schema` now
  agrees and states the default.
