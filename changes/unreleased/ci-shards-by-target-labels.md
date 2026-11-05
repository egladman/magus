### Changed

- **Breaking: a shard's `label` is now `digest`, a digest of its units**, and
  the shortened project list formerly `name` is now `job_name`. The Go fields
  are `Digest` and `JobName`. There is no alias: a workflow reading
  `matrix.label` or `matrix.name` reads nothing, so rename them to
  `matrix.digest` and `matrix.job_name`. Each shard carries `duties`:
  `cache-export` and `notes-verify` each land on exactly one shard.
  `magus run --stdin` refuses a shard whose projects or digest disagree with its
  units.
