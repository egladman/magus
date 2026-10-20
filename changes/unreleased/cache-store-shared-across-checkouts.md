### Changed

- **Every checkout of a repository shares one cache store.** Entries live under
  `<user cache>/magus/cache/<repo>-<hash12>`, so a worktree or clone replays what a sibling
  built at the same content. The run header and `magus doctor` name the store; `cache.dir`
  keeps everything in one directory. GC keeps a blob another process wrote or replayed in
  the last 15 minutes.
