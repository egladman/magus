### Fixed

- **The stale-binary escape keeps its own build.** `go run ./cmd/magus run go-build --no-cache .`
  handed off to the workspace's ./magus, the stale binary it exists to replace, because Go
  1.24 and later run a `go run` build from the build cache rather than a `go-build` temp
  directory. A `go run` build from either place now runs itself.
