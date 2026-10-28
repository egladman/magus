### Changed

- **`magus job fork <id>` with no spec flag forks the job again as declared.** It keeps the
  stored write paths, check, goals and criteria instead of declaring an empty job. Any spec
  flag still declares the job anew.
