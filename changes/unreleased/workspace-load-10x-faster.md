### Fixed

- **Workspace load is faster.** A bare library import is no longer executed as a
  candidate spell, Buzz tokens are shared across sessions, and `magus ls` loads once.
  A run skips re-evaluating a magusfile that does not export the target, and an exact
  source no longer walks the tree.
