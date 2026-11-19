### Fixed

- **The OpenCode wasm builds with the Go release `mise.toml` pins.** `go generate`
  pinned only go.mod's floor, so each machine compiled the embedded runtime with its own
  patch release and regenerated different bytes. The generate line now sets
  `GOTOOLCHAIN`, and a test holds it to `mise.toml`.
