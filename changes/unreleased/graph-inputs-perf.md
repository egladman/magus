### Changed

- **Graph reads fold only the session files written since the last read.** The agent
  contacts `@session` overlay counts are cached beside the session store, keyed by each
  invocation file's name, size, modification time and inode. The store is capped at its
  newest 10,000 invocation files, sparing the newest 500 loaded host sessions, and
  schema-1 files age out like any other.
