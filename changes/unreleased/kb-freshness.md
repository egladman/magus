### Changed

- **A placed checkout starts with a symbol index.** `bootstrap-checkout` and
  `bootstrap-worktree` copy `.magus/symbols` from the checkout that placed them, so its first
  refresh updates an index instead of building one. A seed that fails prints a line and the
  placement carries on.

- **`magus refs`, `magus query` and `magus explain` refresh a stale index in the background.** A read answers
  at once from the index as it stands, says so, and starts one detached refresh for the
  checkout. `--refresh` waits for it and says what it cost, within three minutes. An index that
  failed is named and not retried for two minutes.

- **The stale-index branch of `symbol-search` no longer sends a caller to `magus graph build`.**
  The deny routes to `magus refs`, which refreshes the index itself, and carries no inline
  answer while the index is behind.

- **`duration_ms` on an agent command row is how long the guard took to judge the call.** A
  pre-call hook never sees the call's own run time, so this is not tool time.
