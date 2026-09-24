### Fixed

- **`affected --plan` advice names `--max-shards`, the flag it takes, instead of
  `--ci-max-shards`.** The GitHub Actions guide no longer tells shard jobs to run a
  nonexistent `affected --shard` with a `matrix.total` the plan never emits; they run
  `run ci:gha ${{ matrix.projects }}`.
