### Changed

- **A stale-index search refusal says why the index fell behind.** When the guard refuses
  a symbol search because the symbol index is older than the tree, the refusal now adds
  what magus observed (no server running, a sync in progress, a hook whose binary is
  missing) and the command that keeps the index current.
