### Changed

- **Breaking: the old JSON state files are not read.** For `viewed.json`, `drafts.json`, `seen-threads.json`, `reading.json`, `receipts.json`, `guard-settings.json` and `guard-policy.json` the error names the file and says to `mv` it aside, which drops the read marks, drafts and receipts it held.
