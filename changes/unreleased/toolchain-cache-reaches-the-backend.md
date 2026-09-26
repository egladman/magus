### Fixed

- **`config cache` verbs reach a spell-backed remote tier.** `export` and `import
  --toolchain go --remote`, `prune --remote`, and `query output` publishing and remote
  lookup now carry the workspace's secret resolver, so a backend spell that reads its
  token through `magus\secret` no longer fails with "no secret resolver on this run".
