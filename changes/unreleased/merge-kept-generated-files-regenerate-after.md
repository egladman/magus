### Changed

- **A merge regenerates what it changed.** A generated file the merge driver keeps one
  side of is recorded as owed, and `magus vcs resolve` runs the record once the merge has
  the whole tree. `regenerate-owed` is gone.
