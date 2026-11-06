### Changed

- **A shard plan leaves out units whose declared reads miss the change.** They
  replay from the cache, and the plan lists them under `unreached`.
