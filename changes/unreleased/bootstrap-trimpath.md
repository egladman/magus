### Changed

- **A fresh checkout bootstraps in one command.** `GOEXPERIMENT=jsonv2 go run -trimpath
  ./cmd/magus run go-build --no-cache .` compiles magus and runs its real `go-build`
  target, so the first `./magus` is built like every later one. `--no-cache` skips the
  magus cache for that target; Go's build cache stays on. Every hint that taught the old
  two-step build names it.
