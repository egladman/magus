### Fixed

- **The OpenCode wasm builds with the Go release `mise.toml` pins.** `go generate`
  pinned only go.mod's floor, so each machine compiled the embedded runtime with its own
  patch release and regenerated different bytes. The generate line now sets
  `GOTOOLCHAIN=local`, so the build uses the Go `mise.toml` provides and fails on a
  machine without it instead of downloading one.
