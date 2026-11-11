### Fixed

- **A job row an older magus rewrote reads as its `api_version`.** A line carrying both
  `api_version` and an integer `schema_version` no longer leaves the integer unread, so
  the guard stops refusing every call under the row. A job result stamped
  `schema_version` 2 reads as `magus.job-result/v1alpha1`.
- **Other state an older magus wrote reads forward until the next release.** Version-2
  tokens and session records, `mergequeue.snapshot/v1` snapshots, output records and the
  owed record stamped 1, version-1 tool rows, the knowledge manifest and the workspace
  cache. A rewrite writes only the new names.
