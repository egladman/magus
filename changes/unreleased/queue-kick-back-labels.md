### Added

- **The merge queue labels pull requests with at most one status:**
  `merge-queue: queued`, `merge-queue: kicked back` or `merge-queue: needs regeneration`.
  A merge the queue sees removes every `merge-queue:` label, and the next apply run
  clears a closed pull request still carrying one. Label creation GitHub refuses as
  invalid is an error.
