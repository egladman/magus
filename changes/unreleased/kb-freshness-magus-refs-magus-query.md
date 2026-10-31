### Changed

- **`magus refs`, `magus query` and `magus explain` refresh a stale index in the background.** A read answers
  at once from the index as it stands, says so, and starts one detached refresh for the
  checkout. `--refresh` waits for it and says what it cost, within three minutes. An index that
  failed is named and not retried for two minutes.
