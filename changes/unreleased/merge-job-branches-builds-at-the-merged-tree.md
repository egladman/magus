### Fixed

- **`merge-job-branches --apply` regenerates with a `./magus` built at the merged tree.**
  It used to run the caller's binary, which stalled `symbol-digest-generate`. A failed
  step keeps the tree and names its path, target, output ref and one cause line.
