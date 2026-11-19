### Changed

- **Breaking: the job store's version is its header's `api_version`.** Each migration
  converts `jobs.jsonl` to the next store version, `safetyscissors.job/v1alpha1` through
  `v1alpha11`. Until the next release every write also stamps the `migration` count, so an
  older magus replays nothing, and a header whose count disagrees is rewritten. In
  safetyscissors, `WithMigrations` takes an `apiversion.APIVersion`; `Rows.Migration` is gone.
  [State](https://eli.gladman.cc/magus/concepts/state/#store-migrations).
