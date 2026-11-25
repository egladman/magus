### Fixed

- **A worker in a fresh host worktree can bootstrap its own `./magus`.** The guard
  refused the bootstrap line to a bound worker, so no target could run there.
- **Generators rebuild when the Go code they compile changes.** Their cache keys
  missed the Go tree and replayed output built from older sources.
