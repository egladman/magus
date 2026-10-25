### Changed

- **A job's lint goal judges only the files it writes.** The goal runs
  `magus run lint . -- <files>`, and lint scopes each tool to those files, so another
  session's findings no longer fail a worker. A stale symbol index fails only the files
  changed since it was built. Bare `lint .` still judges the whole tree.
