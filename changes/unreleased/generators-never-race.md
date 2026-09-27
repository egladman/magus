### Fixed

- **A cache replay no longer removes an output before restoring it.** Bytes already
  current are left alone; anything else is staged and renamed over, so a sibling
  compiling a go:embed of the tree never finds a file missing. A declared read whose
  literal prefix names a pruned dir such as `gen/` is now hashed, and MGS4008 sees it.
