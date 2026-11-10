### Fixed

- **A graph read in a magus a target started rebuilds a stale symbol index.** A `magus buzz`
  script under `buzz-test` that called `magus\graph()` after a source edit was refused MGS3007,
  since the outer run holds the project's lock. The nested magus now runs the index op under
  that lock without taking it. An unrelated holder still refuses.
