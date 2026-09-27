### Fixed

- **The docs site build runs under the sandbox again.** `capture()` shelled a command
  out to a temp file and read it back; a composed target's sandbox denies that write
  (MGS2002), failing `site-generate` in the queue gate and PR CI's docs shard. It now
  reads stdout from `proc\exec`'s typed result instead.
