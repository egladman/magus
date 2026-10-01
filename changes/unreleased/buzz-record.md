### Added

- **`magus buzz --record` keeps a script's output and prints a ref to cite.** A quick
  probe run with `--record` lands in the same output store as a target run, so `magus
  query output <ref>` reopens it on any machine sharing the cache. A plan, review comment
  or job's notes can cite the run instead of asserting what it showed.
