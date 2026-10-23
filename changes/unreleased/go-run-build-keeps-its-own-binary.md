### Fixed

- **A `go run` build of magus no longer hands off to the workspace's placed `./magus`.**
  The bootstrap runs `go run ./cmd/magus run go-build` to replace a stale binary, and that
  stale binary used to take over before the fresh build could load the workspace config.
