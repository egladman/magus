### Changed

- **The runtime history is a JSON Lines store at `history/history.jsonl`.** A run appends a
  line per changed entry instead of rewriting the file, and compacts it once it doubles.
  magus refuses to start beside the old `history/v1.json`: move it aside and the history
  refills from the next runs. `magus config history import` and sharded CI histories use
  the new format.
