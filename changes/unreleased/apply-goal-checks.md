### Changed

- **`magus job apply` grades every goal check it adds to a live job.** A goal whose check
  the job did not already carry is held to the guard as a forked one is, so a `human_only`
  target cannot enter a row through apply; a check the row already held is not graded
  again.
- **The footprint cache keys on the attribute files outside the tree.** An edit to the
  repository's `info/attributes` or to `core.attributesFile` is a miss, like an edited
  funcname pattern.
