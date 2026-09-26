### Added

- **Signed Go toolchain bundles.** `magus config cache export --toolchain go --remote`
  signs `GOCACHE` and `GOMODCACHE` into the remote tier; `import --toolchain go
  --remote` restores the newest bundle that verifies against the trust set, so a
  magus miss recompiles only what changed. An unverified bundle is refused.
