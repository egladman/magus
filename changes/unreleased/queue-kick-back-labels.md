### Added

- **The merge queue labels pull requests with at most one status:**
  `queue: queued`, `queue: kicked back` or `queue: needs regeneration`.
  A merge the queue sees removes every `queue:` label, and the next apply run
  clears a closed pull request still carrying one. Label creation GitHub refuses as
  invalid is an error.
