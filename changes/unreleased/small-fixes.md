### Fixed

- **`diagrams-generate docs` no longer replays stale figures after a rebuild.** It
  keys on the digest of the `magus/figure` source the running binary embeds, which
  `magus version -o json` now reports as `embedded.figure_sha256`, so an engine
  change misses the cache without `--no-cache`.
