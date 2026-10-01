### Fixed

- **A reverted edit no longer leaves the symbol index describing the edit.** The scip
  step replayed from cache for the restored sources while the index still held the
  edited ones, so `magus refs` answered stale lines. A replay now requires the index
  its run wrote, and reindexes otherwise.
