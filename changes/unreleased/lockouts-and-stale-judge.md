### Fixed

- **The guard no longer locks a session out of its own recovery.** A checkout's `./magus`
  newer than the hook's binary judges the call even behind HEAD. A refused `job exec` binds
  nobody, a root session's `job exit` releases its row, and `bootstrap-worktree.buzz` leaves
  the harness wiring alone.
