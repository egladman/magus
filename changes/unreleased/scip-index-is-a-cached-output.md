### Changed

- **A checkout replays the symbol index another checkout built.** The scip op records its
  index as its output, keyed by the project's workspace-relative path, so a fresh worktree
  restores it from the shared store. An entry recording none of its step's declared
  outputs no longer replays.
