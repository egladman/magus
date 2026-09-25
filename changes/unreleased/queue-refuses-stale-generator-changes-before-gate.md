### Changed

- **The merge queue kicks back stale generated files before its gate runs.** A change
  that edits generator code must commit outputs that are current on top of the base. If
  they are stale, the kick-back names the stale files and says how to fix them. If they are
  current, the change merges.
