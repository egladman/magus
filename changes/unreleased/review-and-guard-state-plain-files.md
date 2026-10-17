### Changed

- **Breaking: review state and the guard's memos are JSON Lines files, not JSON documents.** An update appends one line, the last line per key is the entry, and a file is compacted once it holds more than twice as many lines as entries, plus 64, the job store's bound.
