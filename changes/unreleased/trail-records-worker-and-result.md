### Added

- **The tool-time row records which worker ran a call and a digest of its result.**
  `magus shell --tool-ms` takes `--call-digest` and `--result-digest`, and the row
  names the thread and subagent. A host that gives no digest is recorded as
  unobserved, never as an empty value, and the result itself is never stored.
