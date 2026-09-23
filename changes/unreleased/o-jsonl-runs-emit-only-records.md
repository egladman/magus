### Changed

- **`-o jsonl` runs emit only records.** Headers, progress, summaries, race diagnostics
  and lock decisions are typed events on stdout beside `run.target.result`; notices, other
  log lines and output printed outside a target are `run.notice` records on stderr.
  `magus x`, `affected --stdin` and `--detach` (`run.detach`) do the same. No record is
  dropped; the schema is now 5.
