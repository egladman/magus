### Changed

- **Breaking: review state and the guard's memos are JSON Lines files, not JSON documents.**
  An update appends one line, the last line per key is the entry, and a file is compacted
  once it holds more than twice as many lines as entries, plus 64, the job store's bound.
  In the cache dir: `review/viewed.jsonl`,
  `review/drafts.jsonl`, `review/seen-comments.jsonl`, `review/reading.jsonl`,
  `review/receipts.jsonl`, `guard-settings.jsonl` and `guard-policy.jsonl`. The old
  `viewed.json`, `drafts.json`, `seen-threads.json`, `reading.json`, `receipts.json`,
  `guard-settings.json` and `guard-policy.json` are not read: the error names the file and
  says to `mv` it aside, which drops the read marks, drafts and receipts it held. The
  check-review job and `magus notes capture` fail on such a file, or on a line that does not
  decode, rather than reading it as no marks.
  `guard-rows.json` is gone; the guard reads the job store on every call.
