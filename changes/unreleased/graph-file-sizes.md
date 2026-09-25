### Added

- **File nodes carry `lines` and `bytes`.** Buzz sources and the files a SCIP index
  defines symbols in are sized from the read that already indexes them, so `magus explain
  file:<path>` says how long a file is. `explain` on a `file:` or `dir:` ID now loads the
  symbol shards. Knowledge-graph schema v15.
