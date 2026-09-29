### Changed

- **A fresh checkout bootstraps in one command.** `go run -trimpath ./cmd/magus run
  go-build --no-cache .` compiles magus and runs its real `go-build` target, so the
  first `./magus` is generated and stamped like every later one, where a bare
  `go build` followed by `./magus run go-build .` linked twice. `--no-cache` skips
  the magus cache, whose key for that target cannot express the embedded-spell
  ordering; Go's build cache stays on, and `-trimpath` matches the target's build so
  packages compile once. Every hint that taught the old link names it.
