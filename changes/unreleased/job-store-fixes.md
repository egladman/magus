### Fixed

- **`magus job wait` accepts a check's own run.** A check is resolved against the
  workspace's `default_charms` the way `magus run` resolves it, so in a workspace whose
  default is `rw`, running `magus run test .` records `test:rw` and satisfies a `test .`
  check. A run of another target, project or charm set is still rejected. A check record
  declaring `no_default_charms: true` opts out: only a `--no-default-charms` run satisfies
  it, which keeps a charmless drift gate declarable, and the command served for it
  carries the flag.
- **A goal's `expect` is optional in the job schema.** The decoder already filled it in
  (`passed` for a check, `changed` for paths and symbols); `magus job fork --schema` now
  agrees and states the default.
