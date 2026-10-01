### Changed

- **`magus refs -o json` carries the index cause.** The answer of `refs`, `refs
  --occurrences` and `refs --definition` holds `index_cause {why, fix}` whenever it reports
  a missing or stale index, the same sentences the text output prints. A name refs cannot
  resolve now prints its record, verdict included, under `-o json`, `-o yaml`, `-o jsonl`
  and `-o template`.
