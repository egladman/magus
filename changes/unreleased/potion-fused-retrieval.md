### Added

- **Knowledge search ranks by embedding beside BM25F.** `magus query`, `magus\query` and
  the console's graph query fuse a fourth list, `embed`, the cosine of each match's static
  embedding with the query's under the potion-code-16M-v2 table. The table is pulled, not
  shipped: `magus graph pull -table` fetches it into the cache, and search never fetches it
  itself. A process reads and verifies the table once, so a fresh pull takes effect in the
  next process; restart the daemon to pick it up there. With no table, or one whose digest
  is not the pin, search ranks as before and the ranking's `skipped` names the `embed`
  list, the reason and the pull command.
