### Changed

- **Breaking: the job store's version is its header's `api_version`.** Each migration
  converts `jobs.jsonl` to the next store version, `safetyscissors.job/v1alpha1` through
  `v1alpha11`, and the `migration` header member is gone. Until the next release a header
  that still counts its migrations is rewritten with its `api_version`. In safetyscissors,
  `WithMigrations` takes an `apiversion.APIVersion` and `Rows.Migration` is removed.
  [State](https://eli.gladman.cc/magus/concepts/state/#store-migrations).
