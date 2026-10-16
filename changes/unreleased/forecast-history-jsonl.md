### Changed

- **The runtime history is a JSON Lines store.** Each line holds one entry: the workspace
  constants, one project's target, one run or one job outcome. A run appends a line for each
  entry it changed instead of rewriting the whole file, and the file is compacted to one line
  per entry once it holds more than twice that plus 64. The old `history/v1.json` document is
  not read: magus names it and refuses to start a fresh history beside it, so move it aside
  with `mv` and the history refills from the next runs. `magus config history import` takes
  the new store's files, and a sharded CI run's per-shard histories are written in the new
  format.
