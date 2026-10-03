### Changed

- **`magus session --brief` caps live leases it names.** Editing jobs come first;
  at most 12 live leases are printed, with an `and N more: magus ls jobs` line for
  the rest, so a dirty store of exited holders cannot refill a compacted window.
