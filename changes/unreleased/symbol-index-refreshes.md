### Fixed

- **A graph read in a magus a target started rebuilds a stale symbol index.** A `magus buzz`
  script under `buzz-test` that called `magus\graph()` after a source edit was refused MGS3007,
  since the outer run holds the project's lock. The nested magus now builds the index op under
  that lock, through the cache, and takes no lock of its own.
