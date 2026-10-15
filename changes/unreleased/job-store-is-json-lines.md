### Changed

- **Breaking: the job store is a JSON Lines file, `jobs.jsonl`.** Each line is one
  version of one row, and a row's last line is the row, so `grep` and `tail` read a
  job's history without magus. A write appends the lines that changed instead of
  rewriting every row, and rewrites the file to one line per row once it holds more
  than twice as many lines as rows, plus 64. Its sidecars, `records-swept.jsonl` and
  `landing-probes.jsonl`, and the archives a clear or delete leaves
  (`jobs-<time>.jsonl`) take the same shape. A `jobs.json` an older magus left is
  refused with an error naming the `mv` that sets it aside; the plan in it is not
  carried over, and neither is a `leases.json` or a cache-dir `ledger/` from before
  that.
