### Fixed

- **`diagrams-generate docs` no longer replays stale figures after a rebuild.** It
  keys on a digest of the running magus binary, which embeds the `magus/figure`
  engine the figures render from, so an engine change misses the cache without
  `--no-cache`.
