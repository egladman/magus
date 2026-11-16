### Fixed

- **A sized gate no longer skips a `ci` member whose declared inputs the change
  misses.** The skip judged the cache key by the declared inputs alone, but the key
  also holds the member's updates and everything it needs, so `format` was skipped
  for Go edits it keys on. Every member that is not narrowed now runs through the
  cache, which replays it when its key did not move.
