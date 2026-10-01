### Added

- **`magus buzz --record` keeps a script's output and prints a ref to cite.** A quick probe
  run with `--record` lands in the same output store as a target run, so `magus query output
  <ref>` reopens it on any machine that shares the cache. A plan, a review comment or a job's
  notes can point at the run instead of asserting what it showed.
