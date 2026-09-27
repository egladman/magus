### Fixed

- **magus reads no longer take `index.lock`.** Every git call runs with
  `GIT_OPTIONAL_LOCKS=0`, so a status from a guard hook, the daemon or the merge driver
  never rewrites the index while your own `git add`, `commit` or `merge` needs it, which
  failed with "index.lock: File exists".
