### Changed

- **The runtime history is a JSON Lines store at `history/history.jsonl`.** A run appends a
  line per changed entry instead of rewriting the file. Loading the history refuses while
  the old `history/v1.json` sits beside it: move it aside and the history refills from the
  next runs. `magus config history import` and sharded CI histories use the new format.
