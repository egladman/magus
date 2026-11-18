### Changed

- **Breaking: the `schema` member of a `-o jsonl` line is the string `magus.report/v1alpha1`.**
  It was the number 5. A script reading `PipeRecord.Schema` gets that string, and a
  reader that expects a number fails on it.
- **Breaking: the `schema` member of a journal stream line is the string
  `safetyscissors.journal-stream/v1alpha1`.** It was the number 1.
- **Breaking: the release index's `schema_version` is the string
  `magus.release-index/v1alpha1`.** It was the number 1. A magus through v0.4.3 cannot
  read the new index, so `magus self update` on it fails until the binary is replaced by
  hand. The reader takes only the new string.
- **The session fold cache, the scripts index and the broker hello and shutdown magic
  carry api version strings.** A cache or index in the numeric form misses once and
  rebuilds. A broker started before the upgrade refuses a new client's hello until it is
  stopped.
