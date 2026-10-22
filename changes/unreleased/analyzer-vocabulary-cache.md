### Fixed

- **A `filenames` finding no longer outlives the rename that fixed it.**
  The analyzer's name now carries a digest of the file-name vocabulary, so
  golangci-lint's per-package cache is invalidated when a file is renamed in
  another package.
