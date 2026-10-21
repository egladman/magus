### Added

- **Knowledge search ranks by embedding beside BM25F.** `magus query` and `magus explain`
  fuse a fourth list, `embed`, the cosine of each match's static embedding with the
  query's under the potion-code-16M-v2 table. The table is pulled, not shipped:
  `magus graph pull -table` fetches it into the cache, and search never fetches it
  itself. With no table, or one whose digest is not the pin, search ranks as before and
  the ranking's `skipped` names the `embed` list, the reason and the pull command.
