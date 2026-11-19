### Changed

- **Breaking: a `-o jsonl` line carries `"api_version":"magus.report/v1alpha1"`.** It was
  `"schema":5`. A script reading `PipeRecord.Schema` reads `PipeRecord.apiVersion` and
  gets that string, and a reader that expects `schema` or a number fails on it.
- **Breaking: a journal stream line carries
  `"api_version":"safetyscissors.journal-stream/v1alpha1"`.** It was `"schema":1`.
- **Breaking: the `magus race` report carries
  `"api_version":"magus.race-report/v1alpha1"`.** It was `"schema":3`.
- **The release index carries `"api_version":"magus.release-index/v1alpha1"`.** The reader
  takes only that member. The writer also emits `"schema_version":1` so a magus through
  v0.4.3 keeps self-updating; `compat(until: no supported release reads schema_version)`.
- **The session fold cache, the scripts index and the broker hello and shutdown magic
  carry api version strings.** The fold cache and scripts index `format` member is
  `api_version`. A cache or index in the numeric form misses once and rebuilds. A broker
  started before the upgrade refuses a new client's hello until it is stopped.
