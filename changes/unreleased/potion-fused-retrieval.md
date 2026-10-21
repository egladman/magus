### Added

- **Knowledge search ranks by embedding beside BM25F.** `magus query` fuses an `embed`
  list under the potion-code-16M-v2 table, which `magus graph pull -table` fetches into
  the cache; search never fetches it. A process reads the table once, so restart the
  daemon after a pull. Without the pinned table, `skipped` names the `embed` list and the
  pull command.
