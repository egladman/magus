### Changed

- **Breaking: `jobs.jsonl` and `attempts.jsonl` hold Kubernetes-shaped records in
  `batch.magus/v1alpha1`.** A row is a `Job` with `metadata`, `spec` and `status`,
  and a try is an `Attempt`. Keys are camelCase and enum values CamelCase. A row's
  `state` is stored as one True condition in `status.conditions`. Both files migrate
  once. A store a job's checkout opens is not migrated: the root migrates it.
- **Breaking: a job has no `holder`.** A server's run is a job with no agent
  section that the server registered. `magus job` JSON and the Buzz `Job` drop
  `holder` and `api_version`, and gain `unknown`, the record members this magus
  does not declare.
- **Breaking: `libs/apiversion`'s `Header` is `TypeMeta`.** `Object` embeds it.
  `Metadata.WithRequires` writes the requires annotation.
