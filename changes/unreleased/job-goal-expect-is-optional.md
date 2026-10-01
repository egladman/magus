### Fixed

- **A goal's `expect` is optional in the job schema.** The decoder already filled it in
  (`passed` for a check, `changed` for paths and symbols); `magus job fork --schema` now
  agrees and states the default.
