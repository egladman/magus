### Changed

- **The footprint cache sits beside the job store.** It is pruned with the store, `--dry-run` reads it without writing to it, and a change to a `diff.<driver>.funcname` or `xfuncname` pattern is a miss.
