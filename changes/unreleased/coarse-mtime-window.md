### Fixed

- **A rewrite in the current timestamp tick is hashed from its bytes.** The mtime
  memo waits until that tick can no longer hide a same-sized write. A source a
  target rewrites, or a stamp another tool replaces, is seen on the next check.
