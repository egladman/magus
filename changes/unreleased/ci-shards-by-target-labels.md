### Changed

- **Breaking: a shard's `label` is a digest of its units**, and the shortened
  project list moved to `name`. Each shard carries `duties`: `cache-export` and
  `notes-verify` each land on exactly one shard. `magus run --stdin` refuses a
  shard whose projects or label disagree with its units.
