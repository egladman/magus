### Fixed

- **A directory that only builds on another platform is no coverage gap.** A package
  guarded by `//go:build js && wasm`, such as `cmd/buzz-playground`, made the coverage
  shard refuse every profile and fail all graph reads. Such directories are now skipped.
