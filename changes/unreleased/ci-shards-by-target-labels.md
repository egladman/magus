### Changed

- **Breaking: a shard's `label` is now `digest`, and `name` is now `job_name`.**
  The Go fields are `Digest` and `JobName`. Workflows rename `matrix.label` to
  `matrix.digest` and `matrix.name` to `matrix.job_name`; there is no alias.
