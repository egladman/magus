### Changed

- **A placed checkout starts with a symbol index.** `bootstrap-checkout` and
  `bootstrap-worktree` copy `.magus/symbols` from the checkout that placed them, so its first
  read updates an index instead of building one. A seed that fails prints a line and the
  placement carries on.

- **`magus refs` and `magus query` refresh the indexes behind their answer.** A stale or
  never-built index is brought current in place, within 60 seconds, and the read says on stderr
  what it refreshed and how long it took. What it cannot refresh it names at error level, and
  the answer comes from the index as it stands. An index that failed is not retried for two
  minutes, so a broken indexer does not charge every read.
