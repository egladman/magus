### Fixed

- **A guard verdict ref resolves from a linked worktree.** A hook run from the primary
  checkout stores a repeated deny's full verdict in that checkout's activity trail, so
  `magus query output grd...` in the worktree failed with MGS8001. It now reads the
  primary checkout's trail when its own has no such payload, and a miss names every cache
  directory searched.
