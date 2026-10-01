### Changed

- **Listings the graph reproduces are refused with the query.** `find`, `fd`, `rg
  --files`, `ls <dir>` and `ls -R <dir>` whose files are all graph file nodes are answered
  by `magus query`, inline, whatever revision the index was built at.
