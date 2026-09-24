### Changed

- **"Session" now means only the host's conversation; magus's per-process id is an
  invocation.** `magus session` lists INVOCATION and SESSION columns; `-o json` keys are
  `invocations`, `invocation` and `session`. The store is schema 2; a schema-1 line is
  counted and named as written before the rename (`legacy` in JSON), and pruning never
  deletes a file this build cannot read.
