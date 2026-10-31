### Changed

- **The stale-index branch of `symbol-search` no longer sends a caller to `magus graph build`.**
  The deny routes to `magus refs`, which refreshes the index itself, and carries no inline
  answer while the index is behind.
