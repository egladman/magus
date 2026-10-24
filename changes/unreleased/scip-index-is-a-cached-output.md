### Changed

- **A checkout replays the symbol index another checkout built.** The scip op records its
  index as its output, at a path keyed by the project's workspace-relative directory
  rather than its absolute one, so a fresh worktree at the same inputs restores the index
  from the shared store instead of running the indexer again. Freshness still holds only
  while the index on disk is the one the entry recorded.
- **An entry that records none of the outputs its step declares no longer replays.** It
  was filed under an older declaration, and its hit would restore nothing.
